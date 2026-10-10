package oidclogin

import (
	"errors"
	"time"
)

const (
	MaxJSONBytes   = 64 << 10
	MaxTokenBytes  = 16 << 10
	MaxCABytes     = 64 << 10
	RequestTimeout = 10 * time.Second
	LoginTimeout   = 5 * time.Minute
	ReviewLifetime = 5 * time.Minute
)

var (
	ErrConfiguration = errors.New("OIDC requires a reviewed HTTPS issuer, public client and credential-free cluster target")
	ErrDiscovery     = errors.New("OIDC discovery is unavailable or unsupported; review issuer and trust explicitly")
	ErrTransport     = errors.New("OIDC request failed; remote response and credentials are not displayed")
	ErrResponse      = errors.New("OIDC response is invalid or exceeds the supported limits")
	ErrVerification  = errors.New("OIDC identity verification failed; no cluster connection was made")
	ErrDenied        = errors.New("OIDC authorization was denied; no cluster connection was made")
	ErrExpired       = errors.New("OIDC review, login or identity expired; review and sign in again")
	ErrInterrupted   = errors.New("OIDC login interrupted; no automatic retry or token persistence")
	ErrUsed          = errors.New("OIDC review or identity already consumed; review and sign in again")
	ErrBrowser       = errors.New("could not open the system browser; review and sign in again")
	ErrListener      = errors.New("cannot bind a private loopback callback for this login")
)
