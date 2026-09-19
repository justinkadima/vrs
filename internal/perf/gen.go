// Package perf generates deterministic fixture repositories for benchmarks
// and end-to-end performance measurements.
package perf

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Spec controls the shape of a generated repository.
type Spec struct {
	Files int   // total tracked files
	Seed  int64 // deterministic content
}

// GenRepo writes a deterministic, source-code-shaped repository into dir.
// File sizes follow a realistic long-tail distribution (mostly small, a few
// large); content is compressible pseudo-source with cross-file repetition,
// so compression and dedup get a fair workout.
func GenRepo(dir string, spec Spec) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	const areas, pkgs = 20, 10
	per := spec.Files / (areas * pkgs)
	if per < 1 {
		per = 1
	}
	rng := rand.New(rand.NewSource(spec.Seed))
	n := 0
	for a := 0; a < areas && n < spec.Files; a++ {
		for p := 0; p < pkgs && n < spec.Files; p++ {
			sub := filepath.Join(dir, "src", fmt.Sprintf("area%02d", a), fmt.Sprintf("pkg%02d", p))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				return err
			}
			for i := 0; i < per && n < spec.Files; i++ {
				content := sourceBlob(rng, drawSize(rng), fmt.Sprintf("area%02d/pkg%02d", a, p), i)
				name := filepath.Join(sub, fmt.Sprintf("file%04d.go", i))
				if err := os.WriteFile(name, content, 0o644); err != nil {
					return err
				}
				n++
			}
		}
	}
	return nil
}

func drawSize(rng *rand.Rand) int {
	switch x := rng.Intn(1000); {
	case x < 780: // 78%: 1–6 KiB
		return 1024 + rng.Intn(5*1024)
	case x < 960: // 18%: 8–60 KiB
		return 8*1024 + rng.Intn(52*1024)
	case x < 999: // 3.9%: 80–300 KiB
		return 80*1024 + rng.Intn(220*1024)
	default: // ~0.1%: 1–2 MiB
		return 1024*1024 + rng.Intn(1024*1024)
	}
}

var corpus = []string{
	"func (s *Server) handle(w http.ResponseWriter, r *http.Request) {",
	"    if err := s.store.Save(ctx, req); err != nil {",
	"        return nil, fmt.Errorf(\"save failed: %w\", err)",
	"    }",
	"    return &Response{ID: %d, Status: \"ok\", Token: \"%s\"}",
	"}",
	"",
	"const cacheTTL = 30 * time.Second",
	"var errNotFound = errors.New(\"not found\")",
	"func (r *Response) Validate() error {",
	"    if r.ID <= 0 { return errNotFound }",
	"    return nil",
	"}",
}

func sourceBlob(rng *rand.Rand, size int, pkg string, idx int) []byte {
	var b []byte
	b = append(b, "package "+strings.ReplaceAll(pkg, "/", "_")+"\n\n"...)
	for len(b) < size {
		b = append(b, corpus[rng.Intn(len(corpus))]...)
		b = append(b, '\n')
	}
	return b[:size]
}

// Mutate appends a line to n randomly chosen tracked files under dir,
// simulating an editing session.
func Mutate(dir string, n int, seed int64) error {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".vrs" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n && len(files) > 0; i++ {
		j := rng.Intn(len(files))
		p := files[j]
		files = append(files[:j], files[j+1:]...) // distinct files
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(f, "// touched %d %d\n", seed, i)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
