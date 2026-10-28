package store

import "context"

// AddRevision stores r with the next number.
func (m *Memory) AddRevision(ctx context.Context, r Revision) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	if err := validateRevision(r); err != nil {
		return Revision{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Revision{}, ErrClosed
	}
	r = normalizeRevision(r)
	r.Number = int64(len(m.revisions) + 1)
	m.revisions = append(m.revisions, r)
	return cloneRevision(r), nil
}

// UpdateRevision sets the status and the error of a revision.
func (m *Memory) UpdateRevision(ctx context.Context, number int64, status RevisionStatus, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validStatus(status) {
		return ErrInvalidRevision
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if number < 1 || number > int64(len(m.revisions)) {
		return ErrNotFound
	}
	m.revisions[number-1].Status = status
	m.revisions[number-1].Error = errMsg
	return nil
}

// LatestApplied returns the applied revision with the highest number.
func (m *Memory) LatestApplied(ctx context.Context) (Revision, bool, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Revision{}, false, ErrClosed
	}
	for i := len(m.revisions) - 1; i >= 0; i-- {
		if m.revisions[i].Status == StatusApplied {
			return cloneRevision(m.revisions[i]), true, nil
		}
	}
	return Revision{}, false, nil
}

// Revision returns one revision.
func (m *Memory) Revision(ctx context.Context, number int64) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Revision{}, ErrClosed
	}
	if number < 1 || number > int64(len(m.revisions)) {
		return Revision{}, ErrNotFound
	}
	return cloneRevision(m.revisions[number-1]), nil
}

// Revisions returns the revisions that match q, newest first.
func (m *Memory) Revisions(ctx context.Context, q RevisionQuery) ([]Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := revisionLimit(q)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := []Revision{}
	for i := len(m.revisions) - 1; i >= 0 && len(out) < limit; i-- {
		if q.Before > 0 && m.revisions[i].Number >= q.Before {
			continue
		}
		out = append(out, cloneRevision(m.revisions[i]))
	}
	return out, nil
}

// Unfinished returns the revisions still pending or loading, oldest first.
func (m *Memory) Unfinished(ctx context.Context) ([]Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := []Revision{}
	for _, r := range m.revisions {
		if r.Status == StatusPending || r.Status == StatusLoading {
			out = append(out, cloneRevision(r))
		}
	}
	return out, nil
}
