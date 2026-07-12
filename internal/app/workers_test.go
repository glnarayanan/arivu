package app

import "testing"

func TestBookmarkJobLeaseCoversBoundedProcessing(t *testing.T) {
	if jobLease < bookmarkProcessingBudget+bookmarkLeaseMargin {
		t.Fatalf("job lease %s must cover bookmark processing budget %s plus margin %s", jobLease, bookmarkProcessingBudget, bookmarkLeaseMargin)
	}
}
