package oidc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/flowcase/flowcase/internal/domain"
	"golang.org/x/oauth2"
)

type ConsumerConfig struct {
	Enabled      bool
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

type Consumer struct {
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauthConfig oauth2.Config
}

func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	oauthConfig := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
	}

	slog.Info("OIDC consumer initialized", "issuer", cfg.IssuerURL, "client_id", cfg.ClientID)

	return &Consumer{
		provider:    provider,
		verifier:    verifier,
		oauthConfig: oauthConfig,
	}, nil
}

func (c *Consumer) AuthURL(state string) string {
	return c.oauthConfig.AuthCodeURL(state)
}

type OIDCUser struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Username string `json:"preferred_username"`
}

func (c *Consumer) Exchange(ctx context.Context, code string) (*OIDCUser, error) {
	token, err := c.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in response")
	}

	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}

	var user OIDCUser
	if err := idToken.Claims(&user); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	if user.Username == "" {
		user.Username = user.Email
	}
	if user.Username == "" {
		user.Username = user.Subject
	}

	return &user, nil
}

// ToUser converts an OIDC user to a domain user for login/creation
func (u *OIDCUser) ToUser() *domain.User {
	return &domain.User{
		Username: u.Username,
		UserType: domain.UserOIDC,
	}
}
