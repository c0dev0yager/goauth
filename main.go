package goauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/c0dev0yager/goauth/internal"
	"github.com/c0dev0yager/goauth/internal/domain"
	"github.com/c0dev0yager/goauth/pkg"
)

type Config struct {
	JwtKey            string
	JwtValidityInMins int
	EncKey            string
	EnvIV             string
}

type authClient struct {
	config Config
	ts     *internal.TokenService
}

var cl *authClient

func NewSingletonClient(
	cf Config,
	rs *redis.Client,
) {
	domain.NewLoggerClient(logrus.InfoLevel)

	tokenConfig := domain.TokenConfig{
		JwtKey:            []byte(cf.JwtKey),
		JwtValidityInMins: time.Duration(cf.JwtValidityInMins) * time.Minute,
		EncKey:            []byte(cf.EncKey),
		EncIV:             []byte(cf.EnvIV),
	}
	cl = &authClient{
		config: cf,
		ts:     internal.NewTokenService(rs, tokenConfig),
	}

	domain.Logger().Infof("%s: ClientInitialised", domain.LogKeyword)
}

func GetClient() *authClient {
	return cl
}

func (cl *authClient) Authenticate(
	next http.Handler,
	roles string,
) http.HandlerFunc {
	return func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		ctx := r.Context()

		logger := pkg.GetFromContext(ctx)
		tv := r.Header.Get("Authorization")
		at, err := cl.ts.Validate(
			ctx,
			tv,
		)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			if errors.Is(err, pkg.ErrAuthTokenExpired) || errors.Is(err, pkg.ErrAuthTokenInvalid) || errors.Is(
				err, pkg.ErrAuthTokenMalformed,
			) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(err.Error())
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(err.Error())
		}
		roleMap := getAuthorizationRoleMap(roles)
		_, found := roleMap[at.Role]
		if !found {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("RoleMismatch")
			return
		}

		ctx = context.WithValue(ctx, AuthIDKey, at.AuthID)
		r = r.WithContext(ctx)

		ctx = context.WithValue(ctx, AuthRoleKey, at.Role)
		r = r.WithContext(ctx)

		headerDTO := GetHeaderDTO(ctx)
		headerDTO.AuthID = string(at.AuthID)

		ctx = context.WithValue(ctx, RequestHeaderContextKey, headerDTO)
		r = r.WithContext(ctx)

		logger.WithField("auth_id", headerDTO.AuthID)
		ctx = context.WithValue(ctx, LoggerContextKey, logger)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	}
}

func (cl *authClient) CreateToken(
	ctx context.Context,
	dto TokenValue,
) (*TokenResponseDTO, error) {
	err := pkg.Validate.Struct(dto)
	if err != nil {
		domain.Logger().Infof("%s: CreateToken Validation: %v", domain.LogKeyword, err)
		return nil, pkg.ErrFieldValidation
	}
	accessTokenDTO := dto.ToInternalToken()
	tokenResponse, err := cl.ts.Create(
		ctx, accessTokenDTO,
	)
	if err != nil {
		return nil, err
	}
	res := TokenResponseDTO{
		AccessToken: pkg.JWTToken(tokenResponse.AccessToken),
		RefreshKey:  tokenResponse.RefreshKey,
		ExpiresAt:   tokenResponse.ExpiresAt,
	}
	return &res, nil
}

func (cl *authClient) RefreshToken(
	ctx context.Context,
	refreshKey string,
	accessToken pkg.JWTToken,
) (*TokenResponseDTO, error) {
	if refreshKey == "" || accessToken == "" {
		return nil, pkg.ErrFieldValidation
	}

	tokenResponse, err := cl.ts.Refresh(
		ctx, refreshKey, string(accessToken),
	)
	if err != nil {
		return nil, err
	}
	res := TokenResponseDTO{
		AccessToken: pkg.JWTToken(tokenResponse.AccessToken),
		RefreshKey:  tokenResponse.RefreshKey,
		ExpiresAt:   tokenResponse.ExpiresAt,
	}
	return &res, nil
}

func (cl *authClient) Validate(
	ctx context.Context,
	accessToken pkg.JWTToken,
) (*TokenValue, error) {
	if accessToken == "" {
		return nil, pkg.ErrFieldValidation
	}
	tokenDTO, err := cl.ts.Validate(
		ctx, string(accessToken),
	)
	if err != nil {
		return nil, err
	}
	response := TokenValue{
		AuthID: string(tokenDTO.AuthID),
		Role:   tokenDTO.Role,
	}
	return &response, nil
}

func (cl *authClient) Invalidate(
	ctx context.Context,
	authID string,
) error {
	if authID == "" {
		return pkg.ErrFieldValidation
	}
	err := cl.ts.Invalidate(
		ctx, domain.AuthID(authID),
	)
	return err
}
