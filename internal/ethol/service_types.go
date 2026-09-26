package ethol

import "time"

type SubscriptionPlan string

const (
	PlanDaily   SubscriptionPlan = "daily"
	PlanWeekly  SubscriptionPlan = "weekly"
	PlanMonthly SubscriptionPlan = "monthly"
)

type AccountStatus string

const (
	AccountPending AccountStatus = "pending_approval"
	AccountActive  AccountStatus = "active"
	AccountSuspend AccountStatus = "suspended"
)

type TenantAccount struct {
	ID                 string           `json:"id"`
	Username           string           `json:"username"`
	PasswordCiphertext string           `json:"password_ciphertext"`
	Plan               SubscriptionPlan `json:"plan"`
	Status             AccountStatus    `json:"status"`
	RequestedAt        time.Time        `json:"requested_at"`
	ApprovedAt         time.Time        `json:"approved_at,omitempty"`
	SubscriptionEndsAt time.Time        `json:"subscription_ends_at,omitempty"`
	Revision           int64            `json:"revision"`
}

type TenantAccountView struct {
	ID                 string           `json:"id"`
	Username           string           `json:"username"`
	Plan               SubscriptionPlan `json:"plan"`
	Status             AccountStatus    `json:"status"`
	RequestedAt        time.Time        `json:"requested_at"`
	ApprovedAt         time.Time        `json:"approved_at,omitempty"`
	SubscriptionEndsAt time.Time        `json:"subscription_ends_at,omitempty"`
	Revision           int64            `json:"revision"`
}

type TenantCredential struct {
	AccountID string
	Username  string
	Password  string
	Revision  int64
}

func planDuration(plan SubscriptionPlan) time.Duration {
	switch plan {
	case PlanDaily:
		return 24 * time.Hour
	case PlanWeekly:
		return 7 * 24 * time.Hour
	case PlanMonthly:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

func validPlan(plan SubscriptionPlan) bool {
	return planDuration(plan) > 0
}

func accountView(acc TenantAccount) TenantAccountView {
	return TenantAccountView{
		ID:                 acc.ID,
		Username:           acc.Username,
		Plan:               acc.Plan,
		Status:             acc.Status,
		RequestedAt:        acc.RequestedAt,
		ApprovedAt:         acc.ApprovedAt,
		SubscriptionEndsAt: acc.SubscriptionEndsAt,
		Revision:           acc.Revision,
	}
}
