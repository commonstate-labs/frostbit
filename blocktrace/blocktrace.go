package blocktrace

import (
	"dex/logs"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Field struct {
	Key   string
	Value any
}

func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

type step struct {
	Seq     uint64
	BlockID string
	Name    string
	Dur     time.Duration
	Fields  []Field
}

type trace struct {
	Created time.Time
	Steps   []step
}

type FieldSnapshot struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type StepSnapshot struct {
	Seq          uint64          `json:"seq"`
	Name         string          `json:"name"`
	DurationMs   int64           `json:"duration_ms"`
	DurationText string          `json:"duration"`
	Fields       []FieldSnapshot `json:"fields,omitempty"`
}

var (
	traceMu       sync.Mutex
	traces        = map[uint64]*trace{}
	traceSeq      atomic.Uint64
	traceEnabled  = loadEnabled()
	traceSlow     = loadSlowThreshold()
	maxTraceSlots = 64
)

func Measure(height uint64, blockID, name string, fields ...Field) func(...Field) {
	if !traceEnabled || height == 0 {
		return func(...Field) {}
	}
	startAt := time.Now()
	return func(extra ...Field) {
		all := make([]Field, 0, len(fields)+len(extra))
		all = append(all, fields...)
		all = append(all, extra...)
		Record(height, blockID, name, time.Since(startAt), all...)
	}
}

func Record(height uint64, blockID, name string, dur time.Duration, fields ...Field) {
	if !traceEnabled || height == 0 || name == "" {
		return
	}
	cp := make([]Field, len(fields))
	copy(cp, fields)
	traceMu.Lock()
	t := traces[height]
	if t == nil {
		t = &trace{Created: time.Now()}
		traces[height] = t
		pruneLocked()
	}
	t.Steps = append(t.Steps, step{
		Seq:     traceSeq.Add(1),
		BlockID: blockID,
		Name:    name,
		Dur:     dur,
		Fields:  cp,
	})
	traceMu.Unlock()
}

func Snapshot(height uint64, blockID string) []StepSnapshot {
	if !traceEnabled || height == 0 {
		return nil
	}
	traceMu.Lock()
	t := traces[height]
	var steps []step
	if t != nil {
		steps = append(steps, t.Steps...)
	}
	traceMu.Unlock()
	if len(steps) == 0 {
		return nil
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Seq < steps[j].Seq })
	out := make([]StepSnapshot, 0, len(steps))
	for _, s := range steps {
		if s.BlockID != "" && blockID != "" && s.BlockID != blockID {
			continue
		}
		fields := make([]FieldSnapshot, 0, len(s.Fields))
		for _, f := range s.Fields {
			if f.Key == "" {
				continue
			}
			fields = append(fields, FieldSnapshot{Key: f.Key, Value: formatValue(f.Value)})
		}
		out = append(out, StepSnapshot{Seq: s.Seq, Name: s.Name, DurationMs: durationMs(s.Dur), DurationText: s.Dur.String(), Fields: fields})
	}
	return out
}

func Flush(height uint64, blockID, root string, total time.Duration, err error, fields ...Field) {
	if !traceEnabled || height == 0 {
		return
	}
	traceMu.Lock()
	t := traces[height]
	if t != nil {
		delete(traces, height)
	}
	traceMu.Unlock()
	if err == nil && total < traceSlow {
		return
	}
	steps := []step(nil)
	if t != nil {
		steps = append(steps, t.Steps...)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Seq < steps[j].Seq })
	logs.Warn(
		"[BlockTrace][Slow] root=%s height=%d block=%s total=%s err=%v meta=%s steps=%s top=%s",
		root, height, shortBlock(blockID), total, err, formatFields(fields), formatSteps(steps, blockID), formatTop(steps, blockID),
	)
}

func loadEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEX_BLOCK_TRACE"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func loadSlowThreshold() time.Duration {
	raw := strings.TrimSpace(os.Getenv("DEX_BLOCK_TRACE_MS"))
	if raw == "" {
		return 200 * time.Millisecond
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return 200 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

func pruneLocked() {
	if len(traces) <= maxTraceSlots {
		return
	}
	oldestHeight := uint64(0)
	var oldest time.Time
	for h, t := range traces {
		if oldestHeight == 0 || t.Created.Before(oldest) {
			oldestHeight = h
			oldest = t.Created
		}
	}
	if oldestHeight != 0 {
		delete(traces, oldestHeight)
	}
}

func formatSteps(steps []step, blockID string) string {
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		if s.BlockID != "" && blockID != "" && s.BlockID != blockID {
			continue
		}
		parts = append(parts, formatStep(s))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " > ")
}

func formatTop(steps []step, blockID string) string {
	filtered := make([]step, 0, len(steps))
	for _, s := range steps {
		if s.BlockID != "" && blockID != "" && s.BlockID != blockID {
			continue
		}
		filtered = append(filtered, s)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Dur > filtered[j].Dur })
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}
	parts := make([]string, 0, len(filtered))
	for _, s := range filtered {
		parts = append(parts, fmt.Sprintf("%s=%s", s.Name, s.Dur))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func formatStep(s step) string {
	if len(s.Fields) == 0 {
		return fmt.Sprintf("%s=%s", s.Name, s.Dur)
	}
	return fmt.Sprintf("%s=%s(%s)", s.Name, s.Dur, formatFields(s.Fields))
}

func formatFields(fields []Field) string {
	if len(fields) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", f.Key, formatValue(f.Value)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func formatValue(v any) string {
	switch x := v.(type) {
	case time.Duration:
		return x.String()
	case string:
		if len(x) > 48 {
			return x[:48] + "..."
		}
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

func durationMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func shortBlock(blockID string) string {
	if len(blockID) <= 12 {
		return blockID
	}
	return blockID[:12]
}
