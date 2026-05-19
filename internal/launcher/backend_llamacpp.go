package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// LlamaCpp implements Backend for llama.cpp's llama-server.
type LlamaCpp struct{}

func init() {
	RegisterBackend(&LlamaCpp{})
}

func (b *LlamaCpp) Name() string { return "llamacpp" }

func (b *LlamaCpp) ServerBinary(cfg *Config) string {
	if path, ok := cfg.Servers["llamacpp"]; ok {
		return ExpandTilde(path)
	}
	return "llama-server"
}

func (b *LlamaCpp) ResolveModel(cfg *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}

	var path string
	if filepath.IsAbs(modelRef) {
		path = modelRef
	} else if cfg.ModelsDir != "" {
		path = filepath.Join(cfg.ModelsDir, modelRef)
	} else {
		path = modelRef
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("model not found: %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("model path is a directory: %s", path)
	}

	return path, nil
}

func (b *LlamaCpp) BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string {
	var args []string
	params := &profile.ProfileParams

	if profile.ModelPath != "" {
		args = append(args, "--model", profile.ModelPath)
	}

	if params.Host != nil {
		args = append(args, "--host", *params.Host)
	}
	if params.Port != nil {
		args = append(args, "--port", strconv.Itoa(*params.Port))
	}
	if params.GPULayers != nil {
		args = append(args, "-ngl", strconv.Itoa(*params.GPULayers))
	}
	if params.Threads != nil {
		args = append(args, "-t", strconv.Itoa(*params.Threads))
	}
	if params.ThreadsBatch != nil {
		args = append(args, "-tb", strconv.Itoa(*params.ThreadsBatch))
	}
	if params.BatchSize != nil {
		args = append(args, "-b", strconv.Itoa(*params.BatchSize))
	}
	if params.ContextSize != nil {
		args = append(args, "-c", strconv.Itoa(*params.ContextSize))
	}
	if params.FlashAttn != nil {
		if *params.FlashAttn {
			args = append(args, "-fa", "on")
		} else {
			args = append(args, "-fa", "off")
		}
	}
	if params.ContBatching != nil && *params.ContBatching {
		args = append(args, "-cb")
	}
	if params.Parallel != nil {
		args = append(args, "-np", strconv.Itoa(*params.Parallel))
	}
	if params.Mlock != nil && *params.Mlock {
		args = append(args, "--mlock")
	}
	if params.NoMmap != nil && *params.NoMmap {
		args = append(args, "--no-mmap")
	}
	if params.Embedding != nil && *params.Embedding {
		args = append(args, "--embedding")
	}
	if params.Jinja != nil && *params.Jinja {
		args = append(args, "--jinja")
	}

	args = append(args, profile.ExtraArgs...)

	return args
}
