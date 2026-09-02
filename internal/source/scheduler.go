package source

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/store"
)

// Processor 处理一轮采集结果（由 presence engine 实现）。
type Processor interface {
	ProcessRound(ctx context.Context, rr *RoundResult)
}

// Scheduler 周期性采集 + 单飞 + 每源超时 + 轮次落库 + 源健康标记。
type Scheduler struct {
	cfg     *config.CollectConfig
	store   *store.Store
	sources []Source
	proc    Processor
	log     *slog.Logger

	mu       sync.Mutex
	inFlight bool
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewScheduler(cfg *config.CollectConfig, st *store.Store, sources []Source, proc Processor, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		store:   st,
		sources: sources,
		proc:    proc,
		log:     log,
		stop:    make(chan struct{}),
	}
}

// Start 启动调度循环（立即跑一轮，然后按 interval 周期）。
func (sc *Scheduler) Start() {
	sc.ensureSourceState()
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		sc.tick()
		ticker := time.NewTicker(sc.cfg.Interval.Value())
		defer ticker.Stop()
		for {
			select {
			case <-sc.stop:
				return
			case <-ticker.C:
				sc.tick()
			}
		}
	}()
}

// Stop 停止调度并等待在途轮次结束。
func (sc *Scheduler) Stop() {
	close(sc.stop)
	sc.wg.Wait()
}

func (sc *Scheduler) ensureSourceState() {
	for _, s := range sc.sources {
		if err := sc.store.UpsertSourceState(s.ID(), s.Kind(), s.ID()); err != nil {
			sc.log.Warn("upsert source state", "id", s.ID(), "err", err)
		}
	}
}

func (sc *Scheduler) timeoutFor(s Source) time.Duration {
	switch s.Kind() {
	case "asus-ssh":
		return sc.cfg.SSHTimeout.Value()
	case "tplink-http":
		return sc.cfg.HTTPTimeout.Value()
	default:
		return sc.cfg.RoundTimeout.Value()
	}
}

func (sc *Scheduler) tick() {
	if !sc.beginFlight() {
		// 上一轮未结束：记录一次 skipped 轮
		if _, err := sc.store.InsertProbeRound(time.Now().Unix(), false, true, "previous round still running"); err != nil {
			sc.log.Warn("insert skipped round", "err", err)
		}
		return
	}
	defer sc.endFlight()

	rr := NewRoundResult()
	results := make([]struct {
		id  string
		obs []Observation
		err error
	}, len(sc.sources))

	var wg sync.WaitGroup
	for i, s := range sc.sources {
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			to := sc.timeoutFor(s)
			ctx, cancel := context.WithTimeout(context.Background(), to)
			defer cancel()
			obs, err := s.Collect(ctx)
			results[i].id = s.ID()
			results[i].obs = obs
			results[i].err = err
		}(i, s)
	}
	wg.Wait()

	allOK := true
	for i := range sc.sources {
		id := results[i].id
		if results[i].err != nil {
			allOK = false
			rr.SourceErrs[id] = results[i].err
			rr.BySource[id] = nil // 保证键存在，使 presence 能区分"采集失败"与"采集到 0 台"
			if err := sc.store.MarkSourceFail(id, results[i].err.Error()); err != nil {
				sc.log.Warn("mark source fail", "id", id, "err", err)
			}
			sc.log.Warn("source collect failed", "id", id, "err", results[i].err)
		} else {
			rr.BySource[id] = results[i].obs
			if err := sc.store.MarkSourceOK(id); err != nil {
				sc.log.Warn("mark source ok", "id", id, "err", err)
			}
		}
	}

	rr.FinishedAt = rr.StartedAt // 修正为真实结束；samples 用同 round
	rr.FinishedAt = time.Now().Unix()
	roundID, err := sc.store.InsertProbeRound(rr.StartedAt, allOK, false, summarizeErrs(rr.SourceErrs))
	if err != nil {
		sc.log.Error("insert probe round", "err", err)
	} else {
		for id, obs := range rr.BySource {
			for _, o := range obs {
				if oerr := sc.store.InsertProbeSample(roundID, o.MAC, o.IP, o.Present, o.Confidence, id); oerr != nil {
					sc.log.Warn("insert probe sample", "err", oerr)
				}
			}
		}
	}

	if sc.proc != nil {
		sc.proc.ProcessRound(context.Background(), rr)
	}
}

func (sc *Scheduler) beginFlight() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.inFlight {
		return false
	}
	sc.inFlight = true
	return true
}

func (sc *Scheduler) endFlight() {
	sc.mu.Lock()
	sc.inFlight = false
	sc.mu.Unlock()
}

func (sc *Scheduler) Running() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.inFlight
}

func summarizeErrs(m map[string]error) string {
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for id, e := range m {
		parts = append(parts, fmt.Sprintf("%s: %v", id, e))
	}
	return strings.Join(parts, "; ")
}
