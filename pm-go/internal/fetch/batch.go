package fetch

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type Batch struct {
	Indices             map[string]store.PackageIndex
	Cached, Fetched     int
	ComputedIntegrities map[string]string
}

type Loader func(context.Context, *lockfile.Package) (Result, error)

// Packages imports local sources in dependency-path order, then overlaps
// registry downloads and imports. The installation session supplies its source
// and script policy through load. emit runs serially for newly fetched registry
// packages, allowing a materializer to consume results before the batch ends.
// Every return path joins workers; failed installs cannot leave writers behind.
func Packages(ctx context.Context, packages map[string]*lockfile.Package, concurrency int, load Loader, emit func(context.Context, string, store.PackageIndex) error) (Batch, error) {
	if load == nil {
		return Batch{}, fmt.Errorf("package fetch requires a loader")
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	out := Batch{Indices: map[string]store.PackageIndex{}, ComputedIntegrities: map[string]string{}}
	var registryPaths []string
	for _, path := range slices.Sorted(maps.Keys(packages)) {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		p := packages[path]
		if p == nil {
			return Batch{}, fmt.Errorf("missing package for %s", path)
		}
		if p.Source == nil {
			registryPaths = append(registryPaths, path)
			continue
		}
		result, err := load(ctx, p)
		if err != nil {
			return Batch{}, err
		}
		if result.Index != nil {
			out.Indices[path] = result.Index
		}
	}
	if concurrency <= 0 {
		concurrency = 128
	}
	concurrency = min(concurrency, len(registryPaths))
	type outcome struct {
		path   string
		result Result
		err    error
	}
	jobs, results := make(chan string), make(chan outcome)
	workers.Go(func() {
		defer close(jobs)
		for _, path := range registryPaths {
			select {
			case jobs <- path:
			case <-ctx.Done():
				return
			}
		}
	})
	for range concurrency {
		workers.Go(func() {
			for path := range jobs {
				if ctx.Err() != nil {
					return
				}
				result, err := load(ctx, packages[path])
				select {
				case results <- outcome{path, result, err}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	for range registryPaths {
		select {
		case item := <-results:
			if item.err != nil {
				return Batch{}, item.err
			}
			if item.result.Index == nil {
				return Batch{}, fmt.Errorf("missing package index for %s", item.path)
			}
			out.Indices[item.path] = item.result.Index
			if item.result.Cached {
				out.Cached++
			} else {
				out.Fetched++
				if emit != nil {
					if err := emit(ctx, item.path, item.result.Index); err != nil {
						return Batch{}, err
					}
				}
			}
			if item.result.ComputedIntegrity != nil {
				canonical, _, _ := strings.Cut(item.path, "(")
				out.ComputedIntegrities[canonical] = *item.result.ComputedIntegrity
			}
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		}
	}
	return out, nil
}

// ContextualizeIndices maps streamed canonical indexes to peer variants. A
// source-backed package's canonical source coordinate precedes its semver key.
func ContextualizeIndices(canonical map[string]store.PackageIndex, graph *lockfile.Graph) map[string]store.PackageIndex {
	out := map[string]store.PackageIndex{}
	for path, p := range graph.Packages {
		base, _, _ := strings.Cut(path, "(")
		for _, key := range []string{path, base, p.SpecKey()} {
			if index, ok := canonical[key]; ok {
				out[path] = maps.Clone(index)
				break
			}
		}
	}
	return out
}
