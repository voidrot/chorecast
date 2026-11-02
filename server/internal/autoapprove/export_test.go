package autoapprove

import "context"

// ApproveOne exposes the per-instance step, to test its in-transaction re-check.
func (s *Service) ApproveOne(ctx context.Context, id int64) (bool, error) { return s.approve(ctx, id) }
