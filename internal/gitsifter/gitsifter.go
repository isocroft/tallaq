/*
!

	@INFO:

	Package `gitsifter` walks a repository's history and counts how often the
	public interface (name + parameters + result) of each function changes.
	Functions whose interface keeps changing are unstable abstractions and
	strong technical-debt candidates.

	[Improvements]:

	- history is replayed oldest -> newest along the first-parent chain, so
		"previous" really is the previous version;
	- a file is only re-parsed when its blob hash changed (formerly each pass
		parsed every file at every commit);
	- functions are keyed by file + qualified name (formely functions were
		keyed by bare name, so same-named functions in different files collided);
	- one unparsable file no longer deadlocks or aborts the run (formely a single
		worker/error-channel layout could hang), parsers are closed, and
		content is read with `io.ReadAll(...)` instead of comparing `err.Error(...)`
		to "EOF";
	- the unused `InterfaceChange` type is now the report's event record.
*/
package gitsifter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `InterfaceChange` records one interface change of one function. */
type InterfaceChange struct {
	FunctionID  string    `json:"function_id"` // "<file>:<Qualified.name>"
	File        string    `json:"file"`
	Commit      string    `json:"commit"`
	Author      string    `json:"author"`
	When        time.Time `json:"when"`
	Before      string    `json:"before"` // "(params) -> result"
	After       string    `json:"after"`
	ChangeCount int       `json:"change_count"` // cumulative, including this one
}

/* @HINT: `Options` configures `TrackChanges` */
type Options struct {
	// Workers is the number of parallel parsers (default: NumCPU).
	Workers int
	// MaxCommits limits history to the most recent N commits (0 = all).
	MaxCommits int
	// Since ignores commits older than this time (zero = no limit).
	Since time.Time
	// MaxFileSize skips larger blobs (default 2 MiB).
	MaxFileSize int64
}

/* @HINT: `Report` is the result of `TrackChanges`. */
type Report struct {
	CommitsScanned int               `json:"commits_scanned"`
	FilesParsed    int               `json:"files_parsed"`
	ParseErrors    int               `json:"parse_errors"`
	Counts         map[string]int    `json:"counts"`
	Changes        []InterfaceChange `json:"changes"`
}

/* @HINT: `Ranked` is one entry of `Report.Top(...)`. */
type Ranked struct {
	FunctionID string `json:"function_id"`
	Changes    int    `json:"changes"`
}

/* @HINT: `Top(...)` returns the `n` most frequently changed functions (all when n <= 0). */
func (r *Report) Top(n int) []Ranked {
	out := make([]Ranked, 0, len(r.Counts))
	for id, c := range r.Counts {
		out = append(out, Ranked{FunctionID: id, Changes: c})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Changes != out[j].Changes {
			return out[i].Changes > out[j].Changes
		}
		return out[i].FunctionID < out[j].FunctionID
	})

	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

type funcSig struct {
	hash string
	text string
}

type fileState struct {
	blob  plumbing.Hash
	funcs map[string]funcSig
}

type parsed struct {
	funcs map[string]funcSig
	err   error
}

/*
@HINT:

`TrackChanges(...)` replays the history of the repository at (or above)
`repoPath` and reports function interface changes.
*/
func TrackChanges(ctx context.Context, repoPath string, opts Options) (*Report, error) {
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU()
	}
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = 2 << 20
	}

	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("failed to open repository %s: %w", repoPath, err)
	}

	commits, err := firstParentHistory(repo, opts)
	if err != nil {
		return nil, err
	}

	t := &tracker{
		opts:   opts,
		state:  make(map[string]*fileState),
		report: &Report{Counts: make(map[string]int)},
	}

	for _, c := range commits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := t.processCommit(ctx, c); err != nil {
			return nil, err
		}
		t.report.CommitsScanned++
	}

	return t.report, nil
}

/* @HINT: `firstParentHistory(...)` returns commits oldest -> newest. */
func firstParentHistory(repo *git.Repository, opts Options) ([]*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve HEAD: %w", err)
	}

	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to read HEAD commit: %w", err)
	}

	var commits []*object.Commit
	for {
		if !opts.Since.IsZero() && c.Committer.When.Before(opts.Since) {
			break
		}

		commits = append(commits, c)
		if opts.MaxCommits > 0 && len(commits) >= opts.MaxCommits {
			break
		}
		if c.NumParents() == 0 {
			break
		}

		parent, err := c.Parent(0)
		if err != nil {
			/* @HINT: Shallow clones end with a missing parent object. */
			if errors.Is(err, object.ErrParentNotFound) || errors.Is(err, plumbing.ErrObjectNotFound) {
				break
			}
			return nil, fmt.Errorf("failed to read parent of %s: %w", c.Hash, err)
		}
		c = parent
	}

	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	return commits, nil
}

type tracker struct {
	opts   Options
	state  map[string]*fileState
	report *Report
}

func (t *tracker) processCommit(ctx context.Context, c *object.Commit) error {
	tree, err := c.Tree()
	if err != nil {
		return fmt.Errorf("failed to read tree of %s: %w", c.Hash, err)
	}

	var jobs []*object.File
	present := make(map[string]struct{})

	err = tree.Files().ForEach(func(f *object.File) error {
		if _, ok := langs.FromPath(f.Name); !ok || langs.ShouldSkipPath(f.Name) {
			return nil
		}
		present[f.Name] = struct{}{}

		if st, ok := t.state[f.Name]; ok && st.blob == f.Hash {
			return nil // @NOTE: unchanged blob: so nothing to re-parse
		}
		jobs = append(jobs, f)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to list files of %s: %w", c.Hash, err)
	}

	for name := range t.state {
		if _, ok := present[name]; !ok {
			delete(t.state, name)
		}
	}

	results := t.parseAll(ctx, jobs)
	if err := ctx.Err(); err != nil {
		return err
	}

	for i, f := range jobs {
		res := results[i]
		old := t.state[f.Name]
		t.report.FilesParsed++

		if res.err != nil {
			t.report.ParseErrors++
			/*
				@NOTE:

				Remember the blob so we do not retry it forever; keep the
				last good signatures.
			*/
			var prev map[string]funcSig
			if old != nil {
				prev = old.funcs
			}
			t.state[f.Name] = &fileState{blob: f.Hash, funcs: prev}
			continue
		}

		if old != nil {
			keys := make([]string, 0, len(res.funcs))
			for k := range res.funcs {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, key := range keys {
				prev, existed := old.funcs[key]
				cur := res.funcs[key]
				if !existed || prev.hash == cur.hash {
					continue
				}

				id := f.Name + ":" + key
				t.report.Counts[id]++
				t.report.Changes = append(t.report.Changes, InterfaceChange{
					FunctionID:  id,
					File:        f.Name,
					Commit:      c.Hash.String(),
					Author:      c.Author.Name,
					When:        c.Author.When,
					Before:      prev.text,
					After:       cur.text,
					ChangeCount: t.report.Counts[id],
				})
			}
		}

		t.state[f.Name] = &fileState{blob: f.Hash, funcs: res.funcs}
	}

	return nil
}

/*
@HINT:

`parseAll(...)` parses the given blobs with a bounded worker pool. Results are
index-aligned with jobs, so no shared map or mutex is needed and workers
can never block on an error channel.
*/
func (t *tracker) parseAll(ctx context.Context, jobs []*object.File) []parsed {
	results := make([]parsed, len(jobs))
	if len(jobs) == 0 {
		return results
	}

	workers := t.opts.Workers
	if workers > len(jobs) {
		workers = len(jobs)
	}

	idx := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				results[i] = parseBlob(jobs[i], t.opts.MaxFileSize)
			}
		}()
	}

	for i := range jobs {
		if ctx.Err() != nil {
			break
		}
		idx <- i
	}
	close(idx)
	wg.Wait()

	return results
}

func parseBlob(f *object.File, maxSize int64) parsed {
	if f.Size > maxSize {
		return parsed{err: fmt.Errorf("%s: file too large (%d bytes)", f.Name, f.Size)}
	}

	r, err := f.Reader()
	if err != nil {
		return parsed{err: err}
	}
	defer r.Close()

	src, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return parsed{err: err}
	}

	pf, err := parse.ParseSource(f.Name, src)
	if err != nil {
		return parsed{err: err}
	}
	defer pf.Close()

	scan := pf.Scan()
	out := make(map[string]funcSig, len(scan.Functions))

	for _, fn := range scan.Functions {
		key := fn.Qualified
		for n := 2; ; n++ {
			if _, dup := out[key]; !dup {
				break
			}
			key = fmt.Sprintf("%s#%d", fn.Qualified, n)
		}

		iface := fn.ParamText + " -> " + fn.ResultType
		out[key] = funcSig{hash: hashString(key + "|" + iface), text: strings.TrimSpace(iface)}
	}

	return parsed{funcs: out}
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
