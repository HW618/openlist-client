package browse

import (
	"context"
	"strings"
	"sync"
)

// ListFunc reads the items of the folder at dir.
type ListFunc func(ctx context.Context, dir string) ([]Item, error)

// CrawlLimits bound a crawl, so that looking through a big storage ends.
type CrawlLimits struct {
	// Depth is how many folders deep below the start the crawl goes.
	Depth int
	// Folders is how many folders are read at most.
	Folders int
	// Hits is how many matches end the crawl.
	Hits int
	// Workers is how many folders are read at once.
	Workers int
}

// DefaultCrawl is what a search without a server index looks through.
var DefaultCrawl = CrawlLimits{Depth: 8, Folders: 600, Hits: 500, Workers: 8}

// Terms are the words of a query, lower-cased. An entry matches when its
// name holds every one of them.
func Terms(query string) []string { return strings.Fields(strings.ToLower(query)) }

// Match reports whether name holds all of terms.
func Match(name string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	name = strings.ToLower(name)
	for _, t := range terms {
		if !strings.Contains(name, t) {
			return false
		}
	}
	return true
}

// Crawl finds the entries below dir whose names match query by reading the
// folders, the nearest ones first and several at once: for a server that has
// no search index. progress, when set, hears the hits so far whenever a
// folder adds some (from any goroutine, with a slice of its own). Crawl reads
// as many folders as the limits and ctx allow and returns what it found by
// then; only a failure to read dir itself, with nothing found, is an error.
func Crawl(ctx context.Context, list ListFunc, dir, query string, lim CrawlLimits, progress func([]Item)) ([]Item, error) {
	terms := Terms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if lim.Workers < 1 {
		lim.Workers = 1
	}
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type job struct {
		dir   string
		depth int
	}
	var (
		mu      sync.Mutex
		cond    = sync.NewCond(&mu)
		queue   = []job{{dir, 0}}
		active  int
		folders int
		hits    []Item
		rootErr error
		done    bool
		// the hits go to progress outside mu, which a slow callback would
		// hold up the other workers by; seq keeps an older list from
		// arriving after a newer one
		seq    int
		sent   int
		sendMu sync.Mutex
	)
	send := func(n int, list []Item) {
		sendMu.Lock()
		defer sendMu.Unlock()
		if n > sent {
			sent = n
			progress(list)
		}
	}
	stop := func() { // callers hold mu
		done = true
		cond.Broadcast()
	}
	// a cancelled ctx wakes the workers
	go func() {
		<-ctx.Done()
		mu.Lock()
		stop()
		mu.Unlock()
	}()
	var wg sync.WaitGroup
	for range lim.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				for len(queue) == 0 && active > 0 && !done {
					cond.Wait()
				}
				if done || len(queue) == 0 || folders >= lim.Folders {
					stop()
					mu.Unlock()
					return
				}
				j := queue[0]
				queue = queue[1:]
				active++
				folders++
				mu.Unlock()

				items, err := list(ctx, j.dir)

				mu.Lock()
				active--
				var news []Item
				var newSeq int
				if err != nil {
					if j.depth == 0 {
						rootErr = err
					}
				} else {
					before := len(hits)
					for _, it := range items {
						if Match(it.Name, terms) {
							hits = append(hits, it)
						}
						if it.IsDir && j.depth < lim.Depth {
							queue = append(queue, job{it.Path, j.depth + 1})
						}
					}
					if len(hits) >= lim.Hits {
						hits = hits[:lim.Hits]
						stop()
					}
					if progress != nil && len(hits) > before {
						seq++
						newSeq, news = seq, append([]Item(nil), hits...)
					}
				}
				cond.Broadcast()
				mu.Unlock()
				if news != nil {
					send(newSeq, news)
				}
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(hits) == 0 && rootErr != nil && parent.Err() == nil {
		return nil, rootErr
	}
	return append([]Item(nil), hits...), nil
}

// Merge joins lists of items without repeating a path; an item of an earlier
// list wins.
func Merge(lists ...[]Item) []Item {
	seen := map[string]bool{}
	var out []Item
	for _, list := range lists {
		for _, it := range list {
			if seen[it.Path] {
				continue
			}
			seen[it.Path] = true
			out = append(out, it)
		}
	}
	return out
}
