package providers

import (
	"context"
	"errors"
)

func (p *Prowlarr) Probe(ctx context.Context) error {
	if p.URL == "" || p.Key == "" {
		return errors.New("not configured")
	}
	var result struct {
		Version string `json:"version"`
	}
	if e := jsonCall(ctx, Client, "GET", p.URL+"/api/v1/system/status", nil, map[string]string{"X-Api-Key": p.Key}, &result); e != nil {
		return e
	}
	if result.Version == "" {
		return errors.New("invalid Prowlarr status")
	}
	return nil
}
func (s *Soulseek) Probe(ctx context.Context) error {
	if s.URL == "" || s.Key == "" {
		return errors.New("not configured")
	}
	var result struct {
		Server struct {
			Connected bool `json:"isConnected"`
			LoggedIn  bool `json:"isLoggedIn"`
		} `json:"server"`
	}
	if e := jsonCall(ctx, Client, "GET", s.URL+"/api/v0/application", nil, s.headers(), &result); e != nil {
		return e
	}
	if !result.Server.Connected || !result.Server.LoggedIn {
		return errors.New("Soulseek peer network is offline")
	}
	return nil
}
func (a *ABB) Probe(ctx context.Context) error { return a.VerifyLogin(ctx) }
