package enablebanking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// ConnectionStore is the persistence surface required to complete a consent.
type ConnectionStore interface {
	GetConnectionByReference(ctx context.Context, reference string) (domain.Connection, error)
	UpdateConnectionAuthorization(ctx context.Context, id int64, requisitionID, status, link, institutionID, institutionName, agreementID string) error
	UpdateConnectionConsentExpiry(ctx context.Context, id int64, expiresAt *time.Time) error
}

// AuthFlow completes an Enable Banking consent after the bank redirects the
// user back with an authorization code.
type AuthFlow struct {
	client *Client
	store  ConnectionStore
	logger *slog.Logger
}

// NewAuthFlow constructs an AuthFlow.
func NewAuthFlow(client *Client, store ConnectionStore, logger *slog.Logger) (*AuthFlow, error) {
	if client == nil {
		return nil, errors.New("enablebanking: client is required")
	}
	if store == nil {
		return nil, errors.New("enablebanking: connection store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthFlow{client: client, store: store, logger: logger}, nil
}

// CompleteAuthorization exchanges the redirect code for a session and attaches
// it to the pending connection identified by state. It returns the new session
// ID.
func (f *AuthFlow) CompleteAuthorization(ctx context.Context, code, state string) (string, error) {
	code = strings.TrimSpace(code)
	state = strings.TrimSpace(state)
	if code == "" || state == "" {
		return "", errors.New("enablebanking: authorization code and state are required")
	}
	connection, err := f.store.GetConnectionByReference(ctx, state)
	if err != nil {
		return "", fmt.Errorf("enablebanking: find pending authorization: %w", err)
	}
	session, err := f.client.CreateSession(ctx, code)
	if err != nil {
		return "", fmt.Errorf("enablebanking: exchange authorization code: %w", err)
	}

	institutionName := firstNonEmpty(session.ASPSP.Name, connection.InstitutionName)
	institutionID := firstNonEmpty(session.ASPSP.Name, connection.InstitutionID)
	agreementID := firstNonEmpty(connection.AgreementID, connection.RequisitionID)

	if err := f.store.UpdateConnectionAuthorization(ctx, connection.ID, session.SessionID, "LN",
		connection.Link, institutionID, institutionName, agreementID); err != nil {
		return "", fmt.Errorf("enablebanking: store authorized connection: %w", err)
	}
	if expiresAt := parseConsentExpiry(session.Access.ValidUntil); expiresAt != nil {
		if err := f.store.UpdateConnectionConsentExpiry(ctx, connection.ID, expiresAt); err != nil {
			return "", fmt.Errorf("enablebanking: store consent expiry: %w", err)
		}
	}
	f.logger.Info("bank connection authorized",
		"session_id", session.SessionID, "institution", institutionName, "accounts", len(session.Accounts))
	return session.SessionID, nil
}
