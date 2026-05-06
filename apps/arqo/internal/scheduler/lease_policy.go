package scheduler

import (
	"os"
	"strings"
)

type LeaseExpirePolicy string

const (
	LeaseExpirePolicyFailedReplan LeaseExpirePolicy = "failed_replan"
	LeaseExpirePolicyRetryReady   LeaseExpirePolicy = "retry_ready"
)

func parseLeaseExpirePolicy(raw string) LeaseExpirePolicy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(LeaseExpirePolicyFailedReplan):
		return LeaseExpirePolicyFailedReplan
	case string(LeaseExpirePolicyRetryReady):
		return LeaseExpirePolicyRetryReady
	default:
		return LeaseExpirePolicyFailedReplan
	}
}

func leaseExpirePolicyFromEnv() LeaseExpirePolicy {
	return parseLeaseExpirePolicy(os.Getenv("ARQO_LEASE_EXPIRE_POLICY"))
}
