package codexinspect

import (
	"context"
	"sync"
	"time"

	"github.com/seakee/cpa-manager/usage-service/internal/store"
)

const scheduleSettingKey = "codex_inspection_schedule"
const lastRunSettingKey = "codex_inspection_last_run"

type RuntimeResolver func(context.Context) (RuntimeConfig, bool, error)

type Scheduler struct {
	store          *store.Store
	resolveRuntime RuntimeResolver

	mu        sync.Mutex
	schedule  ScheduleConfig
	running   bool
	lastRunAt int64
	nextRunAt int64
	lastError string
	lastRun   *RunResult
	wake      chan struct{}
}

func NewScheduler(store *store.Store, resolveRuntime RuntimeResolver) *Scheduler {
	return &Scheduler{
		store:          store,
		resolveRuntime: resolveRuntime,
		schedule:       DefaultScheduleConfig(),
		wake:           make(chan struct{}, 1),
	}
}

func DefaultScheduleConfig() ScheduleConfig {
	return ScheduleConfig{Enabled: false, IntervalMinutes: 60, AutoToggle: true}
}

func NormalizeScheduleConfig(config ScheduleConfig) ScheduleConfig {
	if config.IntervalMinutes <= 0 {
		config.IntervalMinutes = 60
	}
	return config
}

func (s *Scheduler) Start(ctx context.Context) {
	if loaded, ok, err := s.LoadSchedule(ctx); err == nil && ok {
		s.mu.Lock()
		s.schedule = loaded
		s.mu.Unlock()
	}
	if loaded, ok, err := s.LoadLastRun(ctx); err == nil && ok {
		s.mu.Lock()
		s.lastRun = &loaded
		s.lastRunAt = loaded.FinishedAt
		s.lastError = loaded.Error
		s.mu.Unlock()
	}
	go s.loop(ctx)
}

func (s *Scheduler) LoadSchedule(ctx context.Context) (ScheduleConfig, bool, error) {
	config := DefaultScheduleConfig()
	ok, err := s.store.LoadJSONSetting(ctx, scheduleSettingKey, &config)
	return NormalizeScheduleConfig(config), ok, err
}

func (s *Scheduler) SaveSchedule(ctx context.Context, config ScheduleConfig) (ScheduleConfig, error) {
	config = NormalizeScheduleConfig(config)
	if err := s.store.SaveJSONSetting(ctx, scheduleSettingKey, config); err != nil {
		return config, err
	}
	s.mu.Lock()
	s.schedule = config
	s.nextRunAt = 0
	s.mu.Unlock()
	s.notify()
	return config, nil
}

func (s *Scheduler) LoadLastRun(ctx context.Context) (RunResult, bool, error) {
	var result RunResult
	ok, err := s.store.LoadJSONSetting(ctx, lastRunSettingKey, &result)
	return result, ok, err
}

func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lastRun *RunResult
	if s.lastRun != nil {
		copy := *s.lastRun
		lastRun = &copy
	}
	return Status{
		Schedule:  s.schedule,
		Running:   s.running,
		LastRunAt: s.lastRunAt,
		NextRunAt: s.nextRunAt,
		LastError: s.lastError,
		LastRun:   lastRun,
	}
}

func (s *Scheduler) RunNow(ctx context.Context) (RunResult, error) {
	return s.run(ctx)
}

func (s *Scheduler) loop(ctx context.Context) {
	for {
		config := s.currentSchedule()
		if !config.Enabled {
			s.setNextRun(0)
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
			continue
		}

		nextRun := time.Now().Add(time.Duration(config.IntervalMinutes) * time.Minute)
		s.setNextRun(nextRun.UnixMilli())
		timer := time.NewTimer(time.Until(nextRun))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-s.wake:
			if !timer.Stop() {
				<-timer.C
			}
			continue
		case <-timer.C:
			_, _ = s.run(ctx)
		}
	}
}

func (s *Scheduler) run(ctx context.Context) (RunResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return RunResult{Error: "previous inspection is still running"}, nil
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	schedule := s.currentSchedule()
	runtime, ok, err := s.resolveRuntime(ctx)
	if err != nil || !ok {
		result := RunResult{Schedule: schedule, StartedAt: time.Now().UnixMilli(), FinishedAt: time.Now().UnixMilli(), Error: "usage service is not configured"}
		if err != nil {
			result.Error = err.Error()
		}
		s.recordRun(ctx, result)
		return result, err
	}

	result, err := Inspect(ctx, runtime, DefaultSettings(), schedule)
	if err != nil && result.Error == "" {
		result.Error = err.Error()
	}
	s.recordRun(ctx, result)
	return result, err
}

func (s *Scheduler) recordRun(ctx context.Context, result RunResult) {
	_ = s.store.SaveJSONSetting(ctx, lastRunSettingKey, result)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = &result
	s.lastRunAt = result.FinishedAt
	s.lastError = result.Error
}

func (s *Scheduler) currentSchedule() ScheduleConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schedule
}

func (s *Scheduler) setNextRun(value int64) {
	s.mu.Lock()
	s.nextRunAt = value
	s.mu.Unlock()
}

func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
