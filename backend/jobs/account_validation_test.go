package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAccountIntakePersistsOnlyVerifiedCredentials(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	r.ValidateAccount = func(_ context.Context, email, password string) (*int, *int64, error) {
		if email != "fixture@example.invalid" || password != "valid-fixture-password" {
			return nil, nil, errors.New("login rejected")
		}
		n := 0
		return &n, nil, nil
	}
	w := call(t, r, "POST", "/accounts", r.GatewayKey, map[string]string{"email": "fixture@example.invalid", "password": "wrong-password"})
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
	accounts, e := r.Accounts(ctx)
	if e != nil || len(accounts) != 0 {
		t.Fatal("unverified account persisted", accounts, e)
	}
	w = call(t, r, "POST", "/accounts", r.GatewayKey, map[string]string{"email": "fixture@example.invalid", "password": "valid-fixture-password"})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	accounts, e = r.Accounts(ctx)
	if e != nil || len(accounts) != 1 {
		t.Fatal(accounts, e)
	}
	w = call(t, r, "POST", "/accounts", r.GatewayKey, map[string]string{"email": "fixture@example.invalid", "password": "wrong-password"})
	if w.Code != 422 {
		t.Fatal("invalid re-submission accepted", w.Code)
	}
	_, password, e := r.AccountCredentials(ctx, accounts[0].ID)
	if e != nil || password != "valid-fixture-password" {
		t.Fatal("existing account changed", e)
	}
	r.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) {
		return nil, nil, errors.New("provider unavailable")
	}
	if e = r.AddAccount(ctx, "another@example.invalid", "unverified"); e == nil {
		t.Fatal("offline verification accepted")
	}
	accounts, e = r.Accounts(ctx)
	if e != nil || len(accounts) != 1 {
		t.Fatal(accounts, e)
	}
}
func TestAccountIntakeUsesFreshVerifierInsteadOfBackgroundProfile(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	r.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) {
		t.Fatal("intake trusted a background session/profile")
		return nil, nil, nil
	}
	calls := 0
	r.ValidateNewAccount = func(context.Context, string, string) (*int, *int64, error) {
		calls++
		return nil, nil, errors.New("fresh login rejected")
	}
	if e := r.AddAccount(ctx, "fixture@example.invalid", "wrong-password"); e == nil || calls != 1 {
		t.Fatal("fresh verification not enforced", e, calls)
	}
	accounts, e := r.Accounts(ctx)
	if e != nil || len(accounts) != 0 {
		t.Fatal(accounts, e)
	}
}
