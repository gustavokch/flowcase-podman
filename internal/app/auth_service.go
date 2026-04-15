package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/flowcase/flowcase/internal/infra/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	accessTokenDuration  = 15 * time.Minute
	refreshTokenDuration = 7 * 24 * time.Hour
)

type AuthService struct {
	store     domain.Store
	jwtSecret []byte
}

func NewAuthService(store domain.Store, cfg *config.Config) *AuthService {
	return &AuthService{
		store:     store,
		jwtSecret: []byte(cfg.JWTSecret),
	}
}

type LoginRequest struct {
	Username string
	Password string
}

func (s *AuthService) Login(ctx context.Context, req LoginRequest) (*domain.TokenPair, error) {
	user, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, domain.ErrUnauthorized
	}

	return s.generateTokenPair(ctx, user)
}

func (s *AuthService) RefreshTokens(ctx context.Context, refreshToken string) (*domain.TokenPair, error) {
	hash := hashToken(refreshToken)
	stored, err := s.store.GetRefreshToken(ctx, hash)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}

	if time.Now().After(stored.ExpiresAt) {
		s.store.DeleteRefreshToken(ctx, stored.ID)
		return nil, domain.ErrUnauthorized
	}

	s.store.DeleteRefreshToken(ctx, stored.ID)

	user, err := s.store.GetUser(ctx, stored.UserID)
	if err != nil {
		return nil, err
	}

	return s.generateTokenPair(ctx, user)
}

func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID) error {
	return s.store.DeleteUserRefreshTokens(ctx, userID)
}

func (s *AuthService) LoginByUser(ctx context.Context, user *domain.User) (*domain.TokenPair, error) {
	return s.generateTokenPair(ctx, user)
}

func (s *AuthService) ValidateAccessToken(tokenStr string) (*domain.Claims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, domain.ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, domain.ErrUnauthorized
	}

	tokenType, _ := claims["type"].(string)
	if tokenType != "access" {
		return nil, domain.ErrUnauthorized
	}

	sub, _ := claims["sub"].(string)
	uid, err := uuid.Parse(sub)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}

	username, _ := claims["username"].(string)

	var perms []domain.Permission
	if rawPerms, ok := claims["permissions"].([]interface{}); ok {
		for _, p := range rawPerms {
			if ps, ok := p.(string); ok {
				perms = append(perms, domain.Permission(ps))
			}
		}
	}

	return &domain.Claims{
		UserID:      uid,
		Username:    username,
		Permissions: perms,
		TokenType:   "access",
	}, nil
}

func (s *AuthService) generateTokenPair(ctx context.Context, user *domain.User) (*domain.TokenPair, error) {
	perms, err := s.store.GetUserPermissions(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	permStrings := make([]string, len(perms))
	for i, p := range perms {
		permStrings[i] = string(p)
	}

	now := time.Now()
	accessClaims := jwt.MapClaims{
		"sub":         user.ID.String(),
		"username":    user.Username,
		"permissions": permStrings,
		"type":        "access",
		"iat":         now.Unix(),
		"exp":         now.Add(accessTokenDuration).Unix(),
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessStr, err := accessToken.SignedString(s.jwtSecret)
	if err != nil {
		return nil, err
	}

	refreshBytes := make([]byte, 32)
	if _, err := uuid.New().MarshalBinary(); err != nil {
		return nil, err
	}
	refreshID := uuid.New()
	copy(refreshBytes, refreshID[:])
	extra := uuid.New()
	copy(refreshBytes[16:], extra[:])
	refreshStr := hex.EncodeToString(refreshBytes)

	refreshHash := hashToken(refreshStr)
	if err := s.store.SaveRefreshToken(ctx, &domain.RefreshToken{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: now.Add(refreshTokenDuration),
	}); err != nil {
		return nil, err
	}

	return &domain.TokenPair{
		AccessToken:  accessStr,
		RefreshToken: refreshStr,
	}, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
