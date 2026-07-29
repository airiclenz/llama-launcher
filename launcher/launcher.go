package launcher

import (
	core "github.com/airiclenz/llama-launcher/internal/launcher"
)

// Config is the parsed launcher configuration: the servers section, the
// defaults and the named profiles. Its methods include ResolveProfile,
// which merges a named profile with the defaults and resolves its model
// path, ProfileNames, which lists the profiles in display order, and
// IsServerEnabled. Obtain one from LoadConfig.
type Config = core.Config

// Profile is a named model configuration exactly as it appears in the
// config file, before merging. Use Config.ResolveProfile to get the merged
// form the lifecycle verbs take.
type Profile = core.Profile

// ProfileParams holds the tunable server parameters — context size, GPU
// layers, host and port, sampling settings. Every field is a pointer so
// that "not set" stays distinct from the zero value across the merge from
// profile to defaults to fallback.
type ProfileParams = core.ProfileParams

// ResolvedProfile is a profile merged with the config defaults and with its
// model path resolved: the form LoadProfile activates.
type ResolvedProfile = core.ResolvedProfile

// RunningInstance is a snapshot of one LLM-server instance found at
// runtime. It is never persisted — every discovery rebuilds it from live
// probes. Addr reports its host:port, Uptime how long it has been up, and
// the Starting flag marks an instance whose address is bound but whose
// health check does not pass yet (ADR-0010).
type RunningInstance = core.RunningInstance

// StopResult reports what a Stop or Unload call did: the instance it acted
// on, whether the server process itself was stopped or only its model
// unloaded, and the steps taken in order. It is non-nil even on error,
// carrying the steps completed before the failure.
type StopResult = core.StopResult

// ProgressFunc receives the lifecycle step transitions of a running verb,
// one call per step, on the calling goroutine. A nil ProgressFunc discards
// them.
type ProgressFunc = core.ProgressFunc

// NoticeFunc receives user-facing notices — config warnings, the ADR-0007
// drift notice — on the calling goroutine. A nil NoticeFunc discards them.
type NoticeFunc = core.NoticeFunc

// ErrConfigNotFound reports that no config file exists at the given path.
// LoadConfig wraps it, so test for it with errors.Is.
var ErrConfigNotFound = core.ErrConfigNotFound

// ErrNotRunning reports that no server was reachable at the target
// address. Stop and Unload wrap it, so test for it with errors.Is.
var ErrNotRunning = core.ErrNotRunning

// ErrStartupTimeout reports that a server LoadProfile started did not
// become healthy inside its wait window. The server was left running and a
// slow model load may still complete, so treat it as "not yet" rather than
// as a failed load and keep observing the address with
// DiscoverRunningInstances. LoadProfile wraps it, so test for it with
// errors.Is.
var ErrStartupTimeout = core.ErrStartupTimeout

// ErrUnsupported reports an operation the platform this program was built
// for cannot perform — on windows, everything that needs unix process
// control; see the package documentation. The affected verbs wrap it, so
// test for it with errors.Is.
var ErrUnsupported = core.ErrUnsupported

// LoadConfig reads, parses and validates the config file at path,
// delivering each non-fatal deprecation warning to notice as raw text —
// one call per warning, nil discards. It returns ErrConfigNotFound when
// the file does not exist. The per-server API keys it finds are pushed
// onto the process-global backend registry, so the last LoadConfig in a
// process wins; see the package documentation.
func LoadConfig(path string, notice NoticeFunc) (*Config, error) {
	return core.LoadConfigNotify(path, notice)
}

// DefaultConfigDir returns the launcher's configuration directory,
// ~/.config/llama-launcher.
func DefaultConfigDir() string {
	return core.DefaultConfigDir()
}

// DefaultConfigPath returns the default config file location, the
// config.yaml inside DefaultConfigDir.
func DefaultConfigPath() string {
	return core.DefaultConfigPath()
}

// DiscoverRunningInstances probes every backend address the config implies
// and returns one instance per reachable server, including instances that
// are still starting up. Probes run in parallel; unreachable addresses are
// omitted rather than reported as errors. Safe to call concurrently.
func DiscoverRunningInstances(cfg *Config) []*RunningInstance {
	return core.DiscoverRunningInstances(cfg)
}

// LoadProfile activates a resolved profile at its target address and
// reports the instance and whether a server was actually started. When a
// server is already serving the requested model the call is idempotent
// (ADR-0007): matching parameters do nothing, drifted ones deliver a
// single drift notice to notice and leave the server alone — pass
// restart=true to force re-activation instead. Progress steps go to
// progress; both callbacks may be nil.
//
// The call blocks: up to ~30 s waiting for the new server to report
// healthy, plus the stop escalation when a restart displaces an occupant.
// Call it from a goroutine, and cancel an in-flight load with Stop on the
// same address (ADR-0010). Serialize concurrent lifecycle calls against
// one address yourself.
func LoadProfile(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc, notice NoticeFunc) (*RunningInstance, bool, error) {
	return core.LoadProfileNotify(cfg, profile, restart, progress, notice)
}

// Stop stops whatever LLM-server instance is listening at addr, whether or
// not this launcher started it (ADR-0001), and returns the steps taken. It
// returns ErrNotRunning when nothing is reachable there. The call blocks
// for the SIGTERM → SIGKILL → port-release escalation, up to ~20 s.
func Stop(addr string) (*StopResult, error) {
	return core.Stop(addr)
}

// Unload unloads the model of the named backend at addr and returns the
// steps taken. On a managed backend, whose model is baked into its process
// arguments, unloading means stopping the server (ADR-0003, ADR-0004); an
// external backend gets an API unload and keeps running. StopResult's
// ServerStopped field distinguishes the two outcomes. It returns
// ErrNotRunning when nothing is reachable at addr, and blocks for the same
// worst case as Stop.
func Unload(backend, addr string) (*StopResult, error) {
	return core.Unload(backend, addr)
}
