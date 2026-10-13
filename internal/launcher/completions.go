package launcher

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// completeCommand is the hidden subcommand the completion scripts call on
// every tab press. It is absent from printUsage: it is the scripts' interface,
// not the user's.
const completeCommand = "__complete"

// completionSubcommands are the top-level commands offered as the first word,
// in printUsage order.
var completionSubcommands = []string{
	"load", "unload", "start", "stop", "status", "list", "logs", "config", "version", "completions",
}

// completionShells are the shells `completions` prints a script for.
var completionShells = []string{"bash", "zsh", "fish"}

// completionScripts holds the script `completions <shell>` prints. Each one
// hands the words typed so far, the word under the cursor last, to
// `llama-launcher __complete` and offers the lines it prints.
var completionScripts = map[string]string{
	// bash: only bash-3.2 builtins — macOS /bin/bash is 3.2 — so the candidates
	// go through compgen into COMPREPLY rather than mapfile/readarray. -o
	// default falls back to file names when no candidate matches, which is
	// what the path after --config wants.
	"bash": `# bash completion for llama-launcher
_llama_launcher() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local candidates
    candidates="$(llama-launcher __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null)"
    COMPREPLY=( $(compgen -W "$candidates" -- "$cur") )
}
complete -o default -F _llama_launcher llama-launcher
`,
	// zsh: works both from a file named _llama-launcher on $fpath (autoloaded,
	// so funcstack[1] is the file's name) and sourced from the command's
	// output, where only the compdef line takes effect.
	"zsh": `#compdef llama-launcher
# zsh completion for llama-launcher
_llama_launcher() {
    if [[ "${words[CURRENT-1]}" == "--config" ]]; then
        _files
        return
    fi
    local -a candidates
    candidates=(${(f)"$(llama-launcher __complete "${(@)words[2,CURRENT]}" 2>/dev/null)"})
    compadd -a candidates
}
compdef _llama_launcher llama-launcher
if [[ "${funcstack[1]}" == "_llama-launcher" ]]; then
    _llama_launcher "$@"
fi
`,
	// fish: the current token is passed quoted so an empty one still arrives
	// as its own (empty) word.
	"fish": `# fish completion for llama-launcher
function __llama_launcher_complete
    set -l tokens (commandline -opc)
    set -e tokens[1]
    set -l current (commandline -ct)
    llama-launcher __complete $tokens "$current" 2>/dev/null
end
complete -c llama-launcher -f -a '(__llama_launcher_complete)'
complete -c llama-launcher -l config -r -F
`,
}

// cmdCompletions prints the completion script for the shell named in args.
// It never reads, creates or writes a config file: the Homebrew install runs
// it under a throwaway HOME.
func cmdCompletions(args []string) int {
	if len(args) != 1 {
		printCompletionsUsage()
		return 2
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unsupported shell %q\n", args[0])
		printCompletionsUsage()
		return 2
	}
	fmt.Print(script)
	return 0
}

func printCompletionsUsage() {
	fmt.Fprintf(os.Stderr, "Usage: llama-launcher completions %s\n", strings.Join(completionShells, "|"))
}

// cmdComplete is the hidden __complete subcommand. words are the command-line
// words after the program name, the last being the (possibly empty) word under
// the cursor; it prints one candidate for that word per line. It always exits
// 0 and writes nothing to stderr: a tab press must never print an error.
func cmdComplete(words []string) int {
	preceding, current := []string{}, ""
	if len(words) > 0 {
		preceding, current = words[:len(words)-1], words[len(words)-1]
	}

	cmdWords, configPath, wantsPath := stripConfigFlag(preceding)
	if wantsPath {
		// The word under the cursor is --config's path: the shell completes
		// file names itself.
		return 0
	}
	if configPath == "" {
		configPath = os.Getenv("LLAMA_LAUNCHER_CONFIG")
	}
	if configPath == "" {
		configPath = DefaultConfigPath()
	}

	for _, c := range completionCandidates(cmdWords, current, loadCompletionConfig(configPath)) {
		fmt.Println(c)
	}
	return 0
}

// stripConfigFlag removes every `--config <path>` pair from words, returning
// the remaining words and the last path named. wantsPath reports a trailing
// bare --config, whose path is the word still being typed.
func stripConfigFlag(words []string) (rest []string, configPath string, wantsPath bool) {
	for i := 0; i < len(words); i++ {
		if words[i] != "--config" {
			rest = append(rest, words[i])
			continue
		}
		if i+1 >= len(words) {
			return rest, configPath, true
		}
		configPath = words[i+1]
		i++
	}
	return rest, configPath, false
}

// loadCompletionConfig parses and validates the config at path, returning nil
// when it is missing or invalid. It never resolves keys — a tab press must not
// run an api_key_cmd — and never creates the file.
func loadCompletionConfig(path string) *Config {
	cfg, err := parseConfig(path)
	if err != nil {
		return nil
	}
	if err := cfg.validate(); err != nil {
		return nil
	}
	return cfg
}

// completionCandidates returns the candidates for current, given the command
// words typed before it (--config pairs already removed). A word starting with
// "-" is completed from the command's flags, any other from its positional
// values. cfg may be nil, in which case profile and backend names are omitted.
func completionCandidates(cmdWords []string, current string, cfg *Config) []string {
	var flags, values []string
	if len(cmdWords) == 0 {
		flags = []string{"--config"}
		values = completionSubcommands
	} else {
		flags, values = commandCandidates(cmdWords[0], cmdWords[1:], cfg)
	}

	pool := values
	if strings.HasPrefix(current, "-") {
		pool = flags
	}
	var matches []string
	for _, c := range pool {
		if strings.HasPrefix(c, current) {
			matches = append(matches, c)
		}
	}
	return matches
}

// commandCandidates returns the flags and positional values of command, given
// the words typed after it.
func commandCandidates(command string, args []string, cfg *Config) (flags, values []string) {
	positional := hasPositional(args)
	last := ""
	if len(args) > 0 {
		last = args[len(args)-1]
	}

	switch command {
	case "load":
		flags = []string{"--restart", "-r", "--force"}
		if !positional {
			values = completionProfiles(cfg)
		}
	case "unload":
		if !positional {
			values = completionProfiles(cfg)
		}
	case "start":
		if last == "--profile" || last == "-p" {
			return nil, completionProfiles(cfg)
		}
		flags = []string{"--profile", "-p"}
	case "stop":
		if !positional {
			values = completionBackends(cfg)
		}
	case "status", "list":
		flags = []string{"--json"}
	case "logs":
		if len(args) > 0 && args[0] == "clean" {
			if last == "--days" {
				return nil, nil
			}
			return []string{"--days", "--all"}, nil
		}
		flags = []string{"--follow", "-f"}
		if !positional {
			values = append([]string{"clean"}, completionBackends(cfg)...)
		}
	case "config":
		if len(args) == 0 {
			return nil, []string{"validate", "init", "reset"}
		}
		if args[0] == "init" {
			flags = []string{"--force", "-f"}
		}
	case "completions":
		if !positional {
			values = completionShells
		}
	}
	return flags, values
}

// hasPositional reports whether args holds a word that is not a flag.
func hasPositional(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

func completionProfiles(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.ProfileNames()
}

// completionBackends returns the enabled backend names, sorted.
func completionBackends(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	for name := range cfg.Servers {
		if cfg.IsServerEnabled(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
