package api

import (
	"bufio"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eric/nas-filehub/internal/hwinfo"
	"github.com/eric/nas-filehub/internal/thumb"
)

type quotaTotals struct {
	UsedBytes      int64 `json:"usedBytes"`
	AssignedBytes  int64 `json:"assignedBytes"`
	UnlimitedUsers int   `json:"unlimitedUsers"`
	LimitedUsers   int   `json:"limitedUsers"`
	UserCount      int   `json:"userCount"`
	// Percent is used/assigned * 100 when at least one user has a finite quota; otherwise omitted.
	Percent *float64 `json:"percent,omitempty"`
}

type userQuotaUsage struct {
	Used  int64
	Quota int64
}

func summarizeUserQuotas(rows []userQuotaUsage) quotaTotals {
	var t quotaTotals
	t.UserCount = len(rows)
	for _, r := range rows {
		if r.Used > 0 {
			t.UsedBytes += r.Used
		}
		if r.Quota > 0 {
			t.AssignedBytes += r.Quota
			t.LimitedUsers++
		} else {
			t.UnlimitedUsers++
		}
	}
	if t.AssignedBytes > 0 {
		p := math.Round(1000*float64(t.UsedBytes)/float64(t.AssignedBytes)) / 10
		if p < 0 {
			p = 0
		}
		t.Percent = &p
	}
	return t
}

var cpuMu sync.Mutex
var cpuLastTotal, cpuLastIdle uint64
var cpuLastAt time.Time

func readProcStat() (total, idle uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return 0, 0, err
		}
		return 0, 0, os.ErrInvalid
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, os.ErrInvalid
	}
	var nums []uint64
	for _, f := range fields[1:] {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		nums = append(nums, n)
	}
	// user nice system idle iowait irq softirq steal ...
	idle = nums[3]
	if len(nums) > 4 {
		idle += nums[4] // iowait
	}
	for i, n := range nums {
		if i >= 8 {
			break
		}
		total += n
	}
	return total, idle, nil
}

func cpuPercent(sampleWait time.Duration) (float64, bool) {
	total, idle, err := readProcStat()
	if err != nil {
		return 0, false
	}
	cpuMu.Lock()
	havePrev := !cpuLastAt.IsZero() && total >= cpuLastTotal && time.Since(cpuLastAt) < 30*time.Second
	prevTotal, prevIdle := cpuLastTotal, cpuLastIdle
	if !havePrev {
		cpuLastTotal, cpuLastIdle, cpuLastAt = total, idle, time.Now()
		cpuMu.Unlock()
		if sampleWait <= 0 {
			return 0, false
		}
		time.Sleep(sampleWait)
		total2, idle2, err := readProcStat()
		if err != nil {
			return 0, false
		}
		cpuMu.Lock()
		cpuLastTotal, cpuLastIdle, cpuLastAt = total2, idle2, time.Now()
		cpuMu.Unlock()
		return usageFromDelta(total, idle, total2, idle2)
	}
	cpuLastTotal, cpuLastIdle, cpuLastAt = total, idle, time.Now()
	cpuMu.Unlock()
	return usageFromDelta(prevTotal, prevIdle, total, idle)
}

func usageFromDelta(t0, i0, t1, i1 uint64) (float64, bool) {
	if t1 <= t0 {
		return 0, false
	}
	dt := t1 - t0
	var didle uint64
	if i1 >= i0 {
		didle = i1 - i0
	}
	busy := dt - didle
	if didle > dt {
		busy = 0
	}
	p := 100 * float64(busy) / float64(dt)
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return math.Round(p*10) / 10, true
}

func readLoadAvg() (l1, l5, l15 float64, ok bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, false
	}
	parts := strings.Fields(string(b))
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	var err1, err5, err15 error
	l1, err1 = strconv.ParseFloat(parts[0], 64)
	l5, err5 = strconv.ParseFloat(parts[1], 64)
	l15, err15 = strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err5 != nil || err15 != nil {
		return 0, 0, 0, false
	}
	return l1, l5, l15, true
}

func readMemInfo() (total, avail uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			total = parseMemKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			avail = parseMemKB(line)
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	return total, avail, true
}

func parseMemKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return n * 1024
}

func (s *Server) collectQuotaTotals() quotaTotals {
	users, err := s.DB.ListUsers()
	if err != nil {
		return quotaTotals{}
	}
	rows := make([]userQuotaUsage, 0, len(users))
	for i := range users {
		u := &users[i]
		rows = append(rows, userQuotaUsage{Used: s.homeUsedBytes(u), Quota: u.QuotaBytes})
	}
	return summarizeUserQuotas(rows)
}

func (s *Server) handleAdminSystemStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	pct, pctOK := cpuPercent(200 * time.Millisecond)
	l1, l5, l15, loadOK := readLoadAvg()
	cpu := map[string]any{
		"cores": runtime.NumCPU(),
	}
	if pctOK {
		cpu["percent"] = pct
	}
	if loadOK {
		cpu["load1"] = l1
		cpu["load5"] = l5
		cpu["load15"] = l15
	}

	mem := map[string]any{}
	if total, avail, ok := readMemInfo(); ok {
		var used uint64
		if total >= avail {
			used = total - avail
		}
		mem["totalBytes"] = total
		mem["availableBytes"] = avail
		mem["usedBytes"] = used
		if total > 0 {
			mem["percent"] = math.Round(1000*float64(used)/float64(total)) / 10
		}
	}

	disk := map[string]any{"ok": false}
	root := s.Cfg.DataRoot
	if root != "" {
		disk["path"] = root
		if total, free, used, ok := diskUsage(root); ok {
			disk["ok"] = true
			disk["totalBytes"] = total
			disk["freeBytes"] = free
			disk["usedBytes"] = used
			if total > 0 {
				disk["percent"] = math.Round(1000*float64(used)/float64(total)) / 10
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"cpu":    cpu,
		"memory": mem,
		"quotas": s.collectQuotaTotals(),
		"disk":   disk,
		"gpu":    s.gpuStatus(),
	})
}

func (s *Server) gpuStatus() map[string]any {
	mode := thumb.ParseMode(s.Cfg.ThumbHWAccel, s.Cfg.ThumbVAAPI)
	st := hwinfo.Detect("/", mode != thumb.ModeOff)
	plan := thumb.SelectPlan(mode, st)
	if plan.Note(mode) != "" {
		st.ThumbNote = plan.Note(mode)
	}
	return map[string]any{
		"thumbVAAPI":      st.ThumbVAAPI,
		"thumbHWAccel":    string(mode),
		"devDri":          st.DevDri,
		"renderD128":      st.RenderD128,
		"renderNodes":     st.RenderNodes,
		"devices":         st.Devices,
		"summary":         st.Summary,
		"thumbNote":       st.ThumbNote,
		"canUseVAAPI":     st.CanUseVAAPI && plan.Backend == "vaapi",
		"canUseHardware":  plan.CanUseHardware(),
		"selectedBackend": plan.Backend,
		"selectedDevice":  plan.Device,
		"selectedVendor":  plan.Vendor,
	}
}
