package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/semver"
)

func (a *Agent) actualExecutable() (string, error) {
	p, e := a.dep.CurrentExecutable()
	if e != nil {
		return "", e
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return "", e
	}
	real, e := filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	if real != a.cfg.Executable {
		return "", fmt.Errorf("configured executable differs from the running process")
	}
	return real, nil
}
func stateAbsent(cfg Config) bool {
	_, e := os.Lstat(filepath.Join(cfg.StateDir, "state.json"))
	return os.IsNotExist(e)
}
func (a *Agent) matchesRunning(p Prepared) error {
	actual, e := a.actualExecutable()
	if e != nil {
		return e
	}
	if actual != p.Path {
		return fmt.Errorf("running executable is not the pending candidate")
	}
	if e = a.validatePrepared(p, true); e != nil {
		return e
	}
	if e = a.validateSelected(p); e != nil {
		return e
	}
	v, e := canonicalVersion(a.cfg.Info.Version)
	pv, _ := canonicalVersion(p.Version)
	if e != nil || v != pv || a.cfg.Info.Commit != p.Commit {
		return fmt.Errorf("running version/commit differs from pending candidate")
	}
	return nil
}
func (a *Agent) preferBaseline(p Prepared, actual string) bool {
	if within(filepath.Join(a.cfg.StateDir, "versions"), actual) {
		return false
	}
	v, e := canonicalVersion(a.cfg.Info.Version)
	if e != nil {
		return true
	}
	pv, _ := canonicalVersion(p.Version)
	return semver.Compare(v, pv) >= 0
}

                                                                              
                                                                                
                                                                                       
func Bootstrap(ctx context.Context, cfg Config, dep Dependencies) (retErr error) {
	if stateAbsent(cfg) {
		return nil
	}
	safeFallback := false
	defer func() {
		if retErr != nil && !safeFallback {
			retErr = fmt.Errorf("%w: %v", ErrUnsafeBootstrap, retErr)
		}
	}()
	a, e := New(cfg, dep)
	if e != nil {
		return e
	}
	if e = a.supported(); e != nil {
		safeFallback = true
		return e
	}
	if _, e = canonicalVersion(cfg.Info.Version); e != nil || !digestValid(cfg.Info.Commit, 20) {
		safeFallback = true
		return ErrUnknownBaseline
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = a.checkCache(cfg.StateDir); e != nil {
		return e
	}
	unlock, e := a.dep.AcquireLock(filepath.Join(cfg.StateDir, "updater.lock"))
	if e != nil {
		return e
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	s, _, e := a.readState()
	if e != nil {
		return e
	}
	actual, e := a.actualExecutable()
	if e != nil {
		return e
	}
	var selected *Prepared
	if s.Pending != nil {
		p := s.Pending.Prepared
		if actual == p.Path {
			if s.Pending.Attempt != 1 {
				return fmt.Errorf("pending candidate has no recorded startup attempt")
			}
			return a.matchesRunning(p)
		}
		if a.preferBaseline(p, actual) {
			s.Pending = nil
			if e = a.saveState(s); e != nil {
				return e
			}
		} else if s.Pending.Attempt == 1 {
			s.FailedDigest = p.Digest
			s.Pending = nil
			if e = a.saveState(s); e != nil {
				return e
			}
			a.dep.Logf("unconfirmed update %s quarantined; restoring confirmed selection", p.Version)
		} else if cfg.Enabled {
			s.Pending.Attempt = 1
			if e = a.saveState(s); e != nil {
				return e
			}
			selected = &p
		}
	}
	if selected == nil && s.Stable != nil {
		p := *s.Stable
		if !a.preferBaseline(p, actual) {
			selected = &p
		}
	}
	if selected == nil {
		return nil
	}
	if actual == selected.Path {
		return a.matchesRunning(*selected)
	}
	                                                                             
	unlock()
	locked = false
	e = a.execPrepared(*selected)
	if e == nil {
		return nil
	}
	if s.Pending == nil || s.Pending.Prepared.Path != selected.Path {
		return e
	}
	                                                                           
	                                                                           
	unlock, e2 := a.dep.AcquireLock(filepath.Join(cfg.StateDir, "updater.lock"))
	if e2 != nil {
		return e2
	}
	s.FailedDigest = selected.Digest
	s.Pending = nil
	e2 = a.saveState(s)
	unlock()
	if e2 != nil {
		return e2
	}
	if s.Stable != nil && !a.preferBaseline(*s.Stable, actual) {
		return a.execPrepared(*s.Stable)
	}
	safeFallback = true
	return e
}

func (a *Agent) execPrepared(p Prepared) error {
	if e := a.validateSelected(p); e != nil {
		return e
	}
	if e := a.validatePrepared(p, true); e != nil {
		return e
	}
	restore, e := preserveSecretFDs(a.cfg.SecretFDs)
	if e != nil {
		return e
	}
	e = a.dep.Exec(p.Path, a.dep.Args, a.dep.Env)
	restore()
	return e
}

                                                                               
                                                                                    
func Activate(ctx context.Context, cfg Config, dep Dependencies, p Prepared) error {
	if !cfg.Enabled {
		return fmt.Errorf("automatic activation is disabled")
	}
	a, e := New(cfg, dep)
	if e != nil {
		return e
	}
	if e = a.supported(); e != nil {
		return e
	}
	if e = validateInfo(cfg.Info); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = a.checkCache(cfg.StateDir); e != nil {
		return e
	}
	unlock, e := a.dep.AcquireLock(filepath.Join(cfg.StateDir, "updater.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	s, _, e := a.readState()
	if e != nil {
		return e
	}
	if s.Pending != nil {
		return fmt.Errorf("another update is pending")
	}
	if e = a.validatePrepared(p, true); e != nil {
		return e
	}
	cv, _ := canonicalVersion(cfg.Info.Version)
	pv, _ := canonicalVersion(p.Version)
	if semver.Compare(pv, cv) <= 0 || p.ReleaseID <= s.HighReleaseID {
		return fmt.Errorf("update replay/downgrade rejected")
	}
	if s.HighVersion != "" {
		hv, _ := canonicalVersion(s.HighVersion)
		if semver.Compare(pv, hv) <= 0 {
			return fmt.Errorf("update high-water mark prevents downgrade")
		}
	}
	                                                                        
	                                                                 
	candidate := cfg.Info
	candidate.Version = p.Version
	candidate.Commit = p.Commit
	if e = compatible(cfg.Info, candidate, p.Manifest); e != nil {
		return e
	}
	s.HighReleaseID = p.ReleaseID
	s.HighVersion = pv
	s.Pending = &pendingUpdate{Prepared: p}
	if e = a.saveState(s); e != nil {
		return e
	}
	return a.prune(s, p.Path)
}

                                                                            
                                                                               
func PendingConfirmation(ctx context.Context, cfg Config, dep Dependencies) (bool, error) {
	if stateAbsent(cfg) {
		return false, nil
	}
	a, e := New(cfg, dep)
	if e != nil {
		return false, e
	}
	if e = a.supported(); e != nil {
		return false, nil
	}
	if e = ctx.Err(); e != nil {
		return false, e
	}
	s, _, e := a.readState()
	if e != nil {
		return false, e
	}
	if s.Pending == nil {
		return false, nil
	}
	actual, e := a.actualExecutable()
	if e != nil {
		return false, e
	}
	if actual != s.Pending.Prepared.Path {
		return false, nil
	}
	if s.Pending.Attempt != 1 {
		return false, fmt.Errorf("pending candidate was not bootstrapped")
	}
	if e = a.matchesRunning(s.Pending.Prepared); e != nil {
		return false, e
	}
	return true, nil
}

                                                                         
func Confirm(ctx context.Context, cfg Config, dep Dependencies) error {
	if stateAbsent(cfg) {
		return nil
	}
	a, e := New(cfg, dep)
	if e != nil {
		return e
	}
	if e = a.supported(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = a.checkCache(cfg.StateDir); e != nil {
		return e
	}
	unlock, e := a.dep.AcquireLock(filepath.Join(cfg.StateDir, "updater.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	s, _, e := a.readState()
	if e != nil {
		return e
	}
	if s.Pending == nil {
		return nil
	}
	if s.Pending.Attempt != 1 {
		return fmt.Errorf("pending candidate was not bootstrapped")
	}
	if e = a.matchesRunning(s.Pending.Prepared); e != nil {
		return e
	}
	p := s.Pending.Prepared
	s.Previous = s.Stable
	s.Stable = &p
	s.Pending = nil
	if e = a.saveState(s); e != nil {
		return e
	}
	return a.prune(s, p.Path)
}
