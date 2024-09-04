package goauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/c0dev0yager/goauth/internal"
	"github.com/c0dev0yager/goauth/internal/domain"
)

type Config struct {
	JwtKey string
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

	cl = &authClient{
		config: cf,
		ts:     internal.NewTokenService(rs, cf.JwtKey),
	}

	domain.Logger().Info("GoAuth: ClientInitialised")
}

func GetClient() *authClient {
	return cl
}

func GetFromContext(
	ctx context.Context,
) *logrus.Logger {
	logger, ok := ctx.Value(LoggerContextKey).(logrus.Logger)
	if ok {
		logger.WithField("event", "message")
		return &logger
	}

	newLogger := domain.Logger()
	newLogger.WithField("event", "message")
	return newLogger
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

		logger := GetFromContext(ctx)
		tv := r.Header.Get("Authorization")
		at, err := cl.ts.ValidateAccessToken(
			ctx,
			tv,
		)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			if errors.Is(err, ErrAuthTokenExpired) || errors.Is(err, ErrAuthTokenInvalid) || errors.Is(
				err, ErrAuthTokenMalformed,
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
	dto CreateToken,
) (*TokenResponseDTO, error) {
	accessTokenDTO := dto.ToCreateAccessToken()

	tokenResponse, err := cl.ts.Create(ctx, accessTokenDTO)
	if err != nil {
		return nil, err
	}
	res := TokenResponseDTO{
		AccessToken:  JWTToken(tokenResponse.AccessToken),
		RefreshToken: JWTToken(tokenResponse.RefreshToken),
		ExpiresAt:    tokenResponse.ExpiresAt,
	}
	return &res, nil
}
func AuthenticateMiddleware(
	next http.HandlerFunc,
	roles string,
	topicName string,
) http.HandlerFunc {
	next = recoverHandler(next)
	next = cl.Authenticate(next, roles)
	next = loggerMiddleware(next, topicName)
	next = requestMetaMiddleware(next)
	return next
}

func UnauthenticateMiddleware(
	next http.HandlerFunc,
	topicName string,
) http.HandlerFunc {
	next = recoverHandler(next)
	next = loggerMiddleware(next, topicName)
	next = requestMetaMiddleware(next)
	return next
}
