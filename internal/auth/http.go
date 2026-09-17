package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	sessionCookieName = "controlpanel_session"
	csrfCookieName    = "controlpanel_csrf"
)

type HTTPOptions struct {
	PublicOrigin  string
	SecureCookies bool
	Protected     http.Handler
}

type HTTPHandler struct {
	service *Service
	options HTTPOptions
	router  http.Handler
}

type authContextKey struct{}

type requestAuth struct {
	AuthenticatedSession
	RawToken string
}

func NewHTTPHandler(service *Service, options HTTPOptions) *HTTPHandler {
	handler := &HTTPHandler{service: service, options: options}
	router := chi.NewRouter()
	router.Get("/setup/status", handler.setupStatus)
	router.With(handler.requireOrigin).Post("/setup/initialize", handler.initialize)
	router.With(handler.requireOrigin).Post("/auth/login", handler.login)
	router.Group(func(protected chi.Router) {
		protected.Use(handler.requireSession)
		protected.Get("/auth/me", handler.me)
		protected.With(handler.requireOrigin, handler.requireCSRF).Post("/auth/logout", handler.logout)
		protected.With(handler.requireOrigin, handler.requireCSRF).Put("/auth/password", handler.changePassword)
		if options.Protected != nil {
			protected.Mount("/", handler.requireMutationSecurity(options.Protected))
		}
	})
	handler.router = router
	return handler
}

func (handler *HTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(response, request)
}

func (handler *HTTPHandler) ProtectSession(next http.Handler) http.Handler {
	return handler.requireSession(next)
}

func (handler *HTTPHandler) requireMutationSecurity(next http.Handler) http.Handler {
	secured := handler.requireOrigin(handler.requireCSRF(next))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(response, request)
		default:
			secured.ServeHTTP(response, request)
		}
	})
}

func (handler *HTTPHandler) setupStatus(response http.ResponseWriter, request *http.Request) {
	initialized, err := handler.service.SetupStatus(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to check setup status")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"requires_setup": !initialized})
}

func (handler *HTTPHandler) initialize(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return
	}
	if err := handler.service.Initialize(request.Context(), input.Username, input.Password); err != nil {
		if errors.Is(err, ErrAlreadyInitialized) {
			writeError(response, http.StatusConflict, "already_initialized", "Administrator is already initialized")
			return
		}
		writeError(response, http.StatusBadRequest, "invalid_setup", "Administrator settings are invalid")
		return
	}
	grant, err := handler.service.Login(request.Context(), input.Username, input.Password)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Administrator was created but login failed")
		return
	}
	handler.setAuthCookies(response, grant)
	writeJSON(response, http.StatusCreated, userResponse(grant.User))
}

func (handler *HTTPHandler) login(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return
	}
	grant, err := handler.service.Login(request.Context(), input.Username, input.Password)
	if err != nil {
		writeError(response, http.StatusUnauthorized, "invalid_credentials", "Username or password is invalid")
		return
	}
	handler.setAuthCookies(response, grant)
	writeJSON(response, http.StatusOK, userResponse(grant.User))
}

func (handler *HTTPHandler) me(response http.ResponseWriter, request *http.Request) {
	authenticated := request.Context().Value(authContextKey{}).(requestAuth)
	writeJSON(response, http.StatusOK, userResponse(authenticated.User))
}

func (handler *HTTPHandler) logout(response http.ResponseWriter, request *http.Request) {
	authenticated := request.Context().Value(authContextKey{}).(requestAuth)
	if err := handler.service.Logout(request.Context(), authenticated.RawToken); err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to log out")
		return
	}
	handler.clearAuthCookies(response)
	response.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) changePassword(response http.ResponseWriter, request *http.Request) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return
	}
	authenticated := request.Context().Value(authContextKey{}).(requestAuth)
	if err := handler.service.ChangePassword(request.Context(), authenticated.RawToken, input.CurrentPassword, input.NewPassword); err != nil {
		if errors.Is(err, ErrInvalidCurrentPassword) {
			writeError(response, http.StatusBadRequest, "invalid_current_password", "Current password is invalid")
			return
		}
		writeError(response, http.StatusBadRequest, "invalid_password", "New password is invalid")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "unauthenticated", "Authentication is required")
			return
		}
		authenticated, err := handler.service.Authenticate(request.Context(), cookie.Value)
		if err != nil {
			handler.clearAuthCookies(response)
			writeError(response, http.StatusUnauthorized, "unauthenticated", "Authentication is required")
			return
		}
		ctx := context.WithValue(request.Context(), authContextKey{}, requestAuth{
			AuthenticatedSession: authenticated,
			RawToken:             cookie.Value,
		})
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (handler *HTTPHandler) requireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin != "" {
			if (handler.options.PublicOrigin != "" && origin != handler.options.PublicOrigin) ||
				(handler.options.PublicOrigin == "" && !originMatchesRequest(origin, request)) {
				writeError(response, http.StatusForbidden, "origin_rejected", "Request origin is not allowed")
				return
			}
		} else if !strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "same-origin") {
			writeError(response, http.StatusForbidden, "origin_required", "Request origin is required")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func originMatchesRequest(origin string, request *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return parsed.Host == request.Host
}

func (handler *HTTPHandler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authenticated := request.Context().Value(authContextKey{}).(requestAuth)
		rawToken := request.Header.Get("X-CSRF-Token")
		cookie, err := request.Cookie(csrfCookieName)
		if err != nil || rawToken == "" || cookie.Value != rawToken {
			writeError(response, http.StatusForbidden, "csrf_rejected", "CSRF validation failed")
			return
		}
		hash := sha256.Sum256([]byte(rawToken))
		if subtle.ConstantTimeCompare(hash[:], authenticated.Session.CSRFHash[:]) != 1 {
			writeError(response, http.StatusForbidden, "csrf_rejected", "CSRF validation failed")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (handler *HTTPHandler) setAuthCookies(response http.ResponseWriter, grant SessionGrant) {
	base := http.Cookie{Path: "/", SameSite: http.SameSiteLaxMode, Secure: handler.options.SecureCookies, Expires: grant.ExpiresAt}
	session := base
	session.Name = sessionCookieName
	session.Value = grant.SessionToken
	session.HttpOnly = true
	csrf := base
	csrf.Name = csrfCookieName
	csrf.Value = grant.CSRFToken
	csrf.HttpOnly = false
	http.SetCookie(response, &session)
	http.SetCookie(response, &csrf)
}

func (handler *HTTPHandler) clearAuthCookies(response http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(response, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
			HttpOnly: name == sessionCookieName, Secure: handler.options.SecureCookies, SameSite: http.SameSiteLaxMode,
		})
	}
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func userResponse(user User) map[string]any {
	return map[string]any{"user": map[string]string{"id": user.ID, "username": user.Username}}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "request_id": uuid.NewString(),
	}})
}
