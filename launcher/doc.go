// Package launcher is the public Go API of llama-launcher: a curated facade
// over the launcher core that lets another program drive local LLM servers —
// load a profile, discover what is running, stop or unload it — without
// shelling out to the CLI.
//
// # The documented surface is the contract
//
// The supported API is exactly the symbols documented in this package
// (ADR-0011). The type aliases unavoidably expose every exported method of
// the aliased core types — for example Config's terminal-UI accessors — but
// those extras carry no compatibility promise and may change in any release.
// From v1.6.0 on, changing a documented symbol is a breaking change; adding
// one is a minor bump.
//
// # Notices are callbacks
//
// The library never writes to its host's stderr. Both notice-producing
// paths take a NoticeFunc: LoadConfig delivers one call per non-fatal
// config warning, with the raw warning text and no prefix; LoadProfile
// delivers the ADR-0007 drift notice as a single call carrying the full
// formatted text (header, one line per drifted field, the guidance to
// re-run with restart). A nil sink discards notices. Progress steps use the
// separate ProgressFunc sink, which is likewise safe to leave nil.
//
// Re-read a changed config file by calling LoadConfig again. Do not use
// Config.Reload: it is the CLI's entry point and prints warnings to stderr.
//
// # Verbs block; cancellation is Stop
//
// There is no context.Context in this API. LoadProfile, Stop and Unload run
// to completion on the calling goroutine and can take a while: activation
// waits up to ~30 s for the new server to report healthy, and a restart
// first stops the occupant through the SIGTERM → SIGKILL → port-release
// escalation (up to ~20 s more). Call them from a goroutine if the caller
// has a UI to keep responsive.
//
// To cancel an in-flight load, call Stop on the target address: an instance
// that is still coming up is discoverable and stoppable (ADR-0010), which
// is why the API does not duplicate that with Go-level cancellation.
//
// When the activation wait expires LoadProfile returns an error wrapping
// ErrStartupTimeout: the server was left running, so a later health success
// still completes the load — observe it yourself rather than treating the
// call as a failure.
//
// # Platforms
//
// The package compiles on darwin, linux and windows, and each verb works
// wherever its mechanism exists (ADR-0012). On darwin and on linux
// everything works. On windows everything the launcher drives over HTTP
// works — DiscoverRunningInstances, model load and unload against Ollama or
// LM Studio, and activating a profile on a server that is already running —
// while what needs unix process control does not act: no managed server
// (llama-server, ollama serve) is started there, and a stop that would have
// to signal a PID cannot be carried out. LM Studio is the exception that
// keeps working, since its server start and stop are lms CLI calls.
//
// Match on the sentinel only where a windows refusal preserves it. Starting
// a managed llama-server refuses before it forks and returns an error
// wrapping ErrUnsupported, so errors.Is finds it. Two paths report in their
// own words instead: LoadProfile against a stopped Ollama cannot start the
// daemon and reports the address as not reachable, and a Stop — or an Unload
// that reduces to one on a managed backend — ends with the server still
// reachable and its PID undetermined. Both are plain errors, not wrapped
// sentinels.
//
// # One Config per process
//
// Backends live in a process-global registry and LoadConfig pushes the
// per-server API keys onto them, so the last LoadConfig wins for the whole
// process. Loading two configs with different API keys in one program is
// not supported.
//
// The read verbs — LoadConfig, DefaultConfigDir, DefaultConfigPath,
// DiscoverRunningInstances and the Config accessors — are safe to call
// concurrently. The lifecycle verbs — LoadProfile, Stop, Unload — must be
// serialized per address by the caller; concurrent lifecycle calls against
// the same host:port race each other.
//
// # Scope
//
// The launcher manages servers on the local machine and is not a router
// (ADR-0002): it starts, stops and inspects processes and endpoints, it
// does not proxy inference traffic. Controlling servers on a different
// machine belongs to the MCP adapter (ADR-0008), which is configured as an
// ordinary MCP entry; the in-process library and the remote adapter are
// complementary access paths to the same core, not alternatives.
//
// # Example
//
//	cfg, err := launcher.LoadConfig(launcher.DefaultConfigPath(), func(w string) {
//		log.Printf("config warning: %s", w)
//	})
//	if err != nil {
//		return err
//	}
//	profile, err := cfg.ResolveProfile("qwen-coder")
//	if err != nil {
//		return err
//	}
//	instance, started, err := launcher.LoadProfile(cfg, profile, false,
//		func(step string) { log.Println(step) },
//		func(notice string) { log.Println(notice) },
//	)
package launcher
