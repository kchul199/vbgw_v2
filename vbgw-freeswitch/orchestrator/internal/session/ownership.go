package session

import "context"

// ReleaseServiceOwnership detaches the logical AI slot from an active session
// when the call leaves AI ownership before the session is torn down.
func ReleaseServiceOwnership(ctx context.Context, store Store, s *SessionState) (bool, error) {
	if s == nil {
		return false, nil
	}

	s.mu.RLock()
	leased := s.AllocationState == AllocationLeased || s.SlotID != ""
	s.mu.RUnlock()
	if !leased {
		return false, nil
	}

	if onRelease := s.releaseHook(); onRelease != nil {
		onRelease(s.SessionID)
	}
	s.SetCapacityMetadata("", AllocationReleased, "", "")

	if store == nil {
		return true, nil
	}
	if err := store.SaveSession(ctx, s); err != nil {
		return true, err
	}
	return true, nil
}
