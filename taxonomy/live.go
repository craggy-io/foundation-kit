package taxonomy

import (
	"slices"
	"sync"
	"sync/atomic"
)

// Live is the current taxonomy of a running host. Owners declare and
// withdraw their types at run time; each successful change replaces the
// whole taxonomy, and a failed one leaves the previous taxonomy in place.
// It is safe for concurrent use.
type Live struct {
	mu   sync.Mutex // serialises changes; readers never take it
	sets map[string][]Type
	cur  atomic.Pointer[Taxonomy]
}

// NewLive returns a live taxonomy with no types.
func NewLive() *Live {
	l := &Live{sets: map[string][]Type{}}
	empty, _ := New()
	l.cur.Store(empty)
	return l
}

// Taxonomy returns the current taxonomy. It is never nil.
func (l *Live) Taxonomy() *Taxonomy { return l.cur.Load() }

// Owners returns the owners that currently declare types, sorted.
func (l *Live) Owners() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.sets))
	for o := range l.sets {
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

// Declare replaces everything owner declares with types and publishes the
// result. The types may build on other owners' types. On error nothing
// changes.
func (l *Live) Declare(owner string, types []Type) (*Taxonomy, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, err := l.with(owner, types)
	if err != nil {
		return nil, err
	}
	l.sets[owner] = slices.Clone(types)
	l.cur.Store(t)
	return t, nil
}

// Preview returns the taxonomy Declare would publish, without publishing it.
func (l *Live) Preview(owner string, types []Type) (*Taxonomy, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.with(owner, types)
}

// Withdraw removes everything owner declares. It fails, changing nothing,
// while another owner's types still descend from one of them. Withdrawing
// an owner that declares nothing is not an error.
func (l *Live) Withdraw(owner string) (*Taxonomy, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.sets[owner]; !ok {
		return l.cur.Load(), nil
	}
	t, err := l.build(owner, nil, false)
	if err != nil {
		if e, ok := err.(*Error); ok {
			for i := range e.Diagnostics {
				if e.Diagnostics[i].Code == CodeParentUnknown {
					e.Diagnostics[i].Code = CodeOwnerRequired
					e.Diagnostics[i].Message += " once " + owner + " is withdrawn"
				}
			}
		}
		return nil, err
	}
	delete(l.sets, owner)
	l.cur.Store(t)
	return t, nil
}

func (l *Live) with(owner string, types []Type) (*Taxonomy, error) {
	return l.build(owner, types, true)
}

// build validates the current sets with owner's set replaced by types, or
// left out when include is false. Callers hold l.mu.
func (l *Live) build(owner string, types []Type, include bool) (*Taxonomy, error) {
	sets := make([]Set, 0, len(l.sets)+1)
	for o, ts := range l.sets {
		if o != owner {
			sets = append(sets, Set{Owner: o, Types: ts})
		}
	}
	if include {
		sets = append(sets, Set{Owner: owner, Types: types})
	}
	slices.SortFunc(sets, func(a, b Set) int {
		switch {
		case a.Owner < b.Owner:
			return -1
		case a.Owner > b.Owner:
			return 1
		}
		return 0
	})
	return New(sets...)
}
