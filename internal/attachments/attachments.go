// Package attachments stores receipts and invoices and pairs them with
// transactions using metadata extracted from filenames and captions.
package attachments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// Store is the persistence surface required by the attachment service.
type Store interface {
	CreateAttachment(ctx context.Context, att *domain.Attachment) error
	GetAttachment(ctx context.Context, id int64) (domain.Attachment, error)
	AttachToTransaction(ctx context.Context, attachmentID, transactionID int64) error
	ListUnpairedAttachments(ctx context.Context) ([]domain.Attachment, error)
	ListAttachmentsForTransaction(ctx context.Context, transactionID int64) ([]domain.Attachment, error)
	FindPairingCandidates(ctx context.Context, date time.Time, amountMinor int64, currency string, days int) ([]domain.Transaction, error)
}

// SaveInput describes an uploaded file.
type SaveInput struct {
	Reader         io.Reader
	Filename       string
	ContentType    string
	TransactionID  int64
	Date           *time.Time
	AmountMinor    *int64
	Currency       string
	Source         string
	ExtractionText string
}

// Metadata is the pairing information extracted from a filename or caption.
type Metadata struct {
	Date        *time.Time
	AmountMinor *int64
	Currency    string
}

// Service manages the attachment directory and metadata records.
type Service struct {
	store   Store
	dir     string
	maxSize int64
	now     func() time.Time
}

// New constructs a Service and creates the attachment directory.
func New(store Store, dir string, maxSize int64, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("attachments: store is required")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("attachments: directory is required")
	}
	if maxSize <= 0 {
		maxSize = 10 << 20
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("attachments: create directory: %w", err)
	}
	return &Service{store: store, dir: dir, maxSize: maxSize, now: now}, nil
}

// Save stores a file and attempts automatic pairing.
func (s *Service) Save(ctx context.Context, input SaveInput) (domain.Attachment, error) {
	if input.Reader == nil {
		return domain.Attachment{}, errors.New("attachments: file content is required")
	}
	displayName := sanitizeFilename(input.Filename)
	if displayName == "" {
		displayName = "receipt"
	}
	ext := safeExtension(displayName)
	storageName, file, err := s.createFile(ext)
	if err != nil {
		return domain.Attachment{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(file.Name())
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input.Reader, s.maxSize+1))
	if err != nil {
		_ = file.Close()
		return domain.Attachment{}, fmt.Errorf("attachments: write file: %w", err)
	}
	if err := file.Close(); err != nil {
		return domain.Attachment{}, fmt.Errorf("attachments: close file: %w", err)
	}
	if written == 0 {
		return domain.Attachment{}, errors.New("attachments: file is empty")
	}
	if written > s.maxSize {
		return domain.Attachment{}, fmt.Errorf("attachments: file exceeds the %d byte limit", s.maxSize)
	}

	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" || contentType == "application/octet-stream" {
		if detected := mime.TypeByExtension(ext); detected != "" {
			contentType = detected
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	now := s.now().UTC()
	att := domain.Attachment{
		Filename:    displayName,
		FilePath:    filepath.Base(storageName),
		ContentType: contentType,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		UploadedAt:  now,
		Source:      input.Source,
	}
	if att.Source == "" {
		att.Source = "web"
	}
	if input.TransactionID > 0 {
		id := input.TransactionID
		att.TransactionID = &id
	}

	metadata := ExtractMetadata(strings.Join([]string{displayName, input.ExtractionText}, "\n"), input.Currency)
	if input.Date != nil {
		date := input.Date.UTC()
		metadata.Date = &date
	}
	if input.AmountMinor != nil {
		amount := *input.AmountMinor
		metadata.AmountMinor = &amount
	}
	if strings.TrimSpace(input.Currency) != "" {
		metadata.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	}
	if metadata.Currency == "" {
		metadata.Currency = "EUR"
	}
	att.ExtractedDate = metadata.Date
	att.ExtractedAmountMinor = metadata.AmountMinor
	att.ExtractedCurrency = metadata.Currency

	if err := s.store.CreateAttachment(ctx, &att); err != nil {
		return domain.Attachment{}, err
	}
	cleanup = false

	if att.TransactionID != nil {
		if err := s.store.AttachToTransaction(ctx, att.ID, *att.TransactionID); err != nil {
			att.TransactionID = nil
			return att, fmt.Errorf("attachments: attach to transaction: %w", err)
		}
		return att, nil
	}
	if err := s.autoPair(ctx, &att); err != nil {
		return att, err
	}
	return att, nil
}

func (s *Service) createFile(ext string) (string, *os.File, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", nil, fmt.Errorf("attachments: random name: %w", err)
	}
	name := fmt.Sprintf("%d-%s%s", s.now().UTC().UnixNano(), hex.EncodeToString(random[:]), ext)
	path := filepath.Join(s.dir, name)
	// #nosec G304 -- path is built from a generated basename inside the managed directory.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("attachments: create file: %w", err)
	}
	return name, file, nil
}

func (s *Service) autoPair(ctx context.Context, att *domain.Attachment) error {
	if att.ExtractedDate == nil || att.ExtractedAmountMinor == nil {
		return nil
	}
	candidates, err := s.pairingCandidates(ctx, *att.ExtractedDate, *att.ExtractedAmountMinor, att.ExtractedCurrency)
	if err != nil {
		return err
	}
	if len(candidates) != 1 {
		return nil
	}
	if err := s.store.AttachToTransaction(ctx, att.ID, candidates[0].ID); err != nil {
		return fmt.Errorf("attachments: auto-pair: %w", err)
	}
	id := candidates[0].ID
	att.TransactionID = &id
	return nil
}

func (s *Service) pairingCandidates(ctx context.Context, date time.Time, amount int64, currency string) ([]domain.Transaction, error) {
	seen := make(map[int64]struct{})
	var out []domain.Transaction
	for _, signed := range []int64{amount, -amount} {
		candidates, err := s.store.FindPairingCandidates(ctx, date, signed, currency, 3)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if _, exists := seen[candidate.ID]; exists {
				continue
			}
			seen[candidate.ID] = struct{}{}
			out = append(out, candidate)
		}
	}
	return out, nil
}

// Attach links an attachment to a transaction.
func (s *Service) Attach(ctx context.Context, attachmentID, transactionID int64) error {
	if attachmentID <= 0 || transactionID <= 0 {
		return errors.New("attachments: attachment and transaction IDs are required")
	}
	return s.store.AttachToTransaction(ctx, attachmentID, transactionID)
}

// ListUnpaired returns the receipt inbox.
func (s *Service) ListUnpaired(ctx context.Context) ([]domain.Attachment, error) {
	return s.store.ListUnpairedAttachments(ctx)
}

// RepairUnpaired retries automatic pairing for every receipt in the inbox.
// It is safe to call after each successful bank sync: attachments with no
// exact match or with ambiguous matches remain unpaired.
func (s *Service) RepairUnpaired(ctx context.Context) (int, error) {
	unpaired, err := s.store.ListUnpairedAttachments(ctx)
	if err != nil {
		return 0, err
	}
	repaired := 0
	var errs []error
	for i := range unpaired {
		att := &unpaired[i]
		if att.Paired() {
			continue
		}
		if err := s.autoPair(ctx, att); err != nil {
			errs = append(errs, fmt.Errorf("attachments: repair %d: %w", att.ID, err))
			continue
		}
		if att.Paired() {
			repaired++
		}
	}
	return repaired, errors.Join(errs...)
}

// ListForTransaction returns attachments linked to a transaction.
func (s *Service) ListForTransaction(ctx context.Context, transactionID int64) ([]domain.Attachment, error) {
	return s.store.ListAttachmentsForTransaction(ctx, transactionID)
}

// Open returns metadata and a seekable file for an attachment.
func (s *Service) Open(ctx context.Context, id int64) (domain.Attachment, *os.File, error) {
	att, err := s.store.GetAttachment(ctx, id)
	if err != nil {
		return domain.Attachment{}, nil, err
	}
	base := filepath.Base(att.FilePath)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return domain.Attachment{}, nil, errors.New("attachments: invalid stored file path")
	}
	// #nosec G304 -- only a basename inside the managed directory is opened.
	file, err := os.Open(filepath.Join(s.dir, base))
	if err != nil {
		return domain.Attachment{}, nil, fmt.Errorf("attachments: open file: %w", err)
	}
	return att, file, nil
}

// SaveTelegram stores a document or photo received by the Telegram bot.
func (s *Service) SaveTelegram(ctx context.Context, filename, contentType, caption string, body io.Reader) (domain.Attachment, error) {
	return s.Save(ctx, SaveInput{
		Reader:         body,
		Filename:       filename,
		ContentType:    contentType,
		Source:         "telegram",
		ExtractionText: caption,
	})
}

// SaveWeb stores a browser upload.
func (s *Service) SaveWeb(ctx context.Context, input SaveInput) (domain.Attachment, error) {
	input.Source = "web"
	return s.Save(ctx, input)
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if len(name) > 180 {
		ext := filepath.Ext(name)
		name = name[:180-len(ext)] + ext
	}
	return name
}

func safeExtension(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 10 {
		return ""
	}
	for _, r := range ext {
		if r == '.' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return ""
	}
	return ext
}

const (
	amountPattern        = `([0-9]{1,3}(?:[.,][0-9]{3})*(?:[.,][0-9]{2})?|[0-9]{1,9}(?:[.,][0-9]{2})?)`
	amountDecimalPattern = `([0-9]{1,3}(?:[.,][0-9]{3})*[.,][0-9]{2}|[0-9]{1,9}[.,][0-9]{2})`
)

var (
	dateISO      = regexp.MustCompile(`(?:^|[^0-9])(20\d{2})-(\d{2})-(\d{2})(?:[^0-9]|$)`)
	dateEuropean = regexp.MustCompile(`(?:^|[^0-9])(\d{1,2})[/-](\d{1,2})[/-](20\d{2})(?:[^0-9]|$)`)
	amountLabel  = regexp.MustCompile(`(?i)(?:total|importe|amount|suma)[^0-9\-]{0,24}` + amountPattern)
	amountSymbol = regexp.MustCompile(`(?:€|\$|£)\s*` + amountPattern)
	amountBare   = regexp.MustCompile(`(?:^|[^0-9])` + amountDecimalPattern + `(?:[^0-9]|$)`)
	currencyCode = regexp.MustCompile(`(?i)\b(EUR|USD|GBP|CHF|MXN|ARS|COP|CLP|PEN)\b`)
)

// ExtractMetadata finds a date and amount in arbitrary filename or caption text.
func ExtractMetadata(text, defaultCurrency string) Metadata {
	out := Metadata{Currency: strings.ToUpper(strings.TrimSpace(defaultCurrency))}
	amountText := text
	if match := dateISO.FindStringSubmatch(text); match != nil {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		if validDate(year, month, day) {
			date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			out.Date = &date
		}
		amountText = maskMatch(amountText, match[0])
	}
	if out.Date == nil {
		if match := dateEuropean.FindStringSubmatch(amountText); match != nil {
			day, _ := strconv.Atoi(match[1])
			month, _ := strconv.Atoi(match[2])
			year, _ := strconv.Atoi(match[3])
			if validDate(year, month, day) {
				date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				out.Date = &date
			}
			amountText = maskMatch(amountText, match[0])
		}
	}
	if match := amountLabel.FindStringSubmatch(amountText); match != nil {
		if amount, ok := parseAmount(match[1], out.Currency); ok {
			out.AmountMinor = &amount
		}
	}
	if out.AmountMinor == nil {
		if match := amountSymbol.FindStringSubmatch(amountText); match != nil {
			if amount, ok := parseAmount(match[1], out.Currency); ok {
				out.AmountMinor = &amount
			}
		}
	}
	if out.AmountMinor == nil {
		if match := amountBare.FindStringSubmatch(amountText); match != nil {
			if amount, ok := parseAmount(match[1], out.Currency); ok {
				out.AmountMinor = &amount
			}
		}
	}
	if match := currencyCode.FindStringSubmatch(text); match != nil {
		out.Currency = strings.ToUpper(match[1])
	}
	if out.Currency == "" {
		out.Currency = "EUR"
	}
	return out
}

func maskMatch(text, match string) string {
	if match == "" {
		return text
	}
	return strings.Replace(text, match, strings.Repeat(" ", len(match)), 1)
}

func validDate(year, month, day int) bool {
	if year < 2000 || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return date.Year() == year && int(date.Month()) == month && date.Day() == day
}

func parseAmount(raw, currency string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	hasComma := strings.Contains(value, ",")
	hasDot := strings.Contains(value, ".")
	switch {
	case hasComma && hasDot:
		// Treat the right-most separator as the decimal marker.
		if strings.LastIndex(value, ",") > strings.LastIndex(value, ".") {
			value = strings.ReplaceAll(value, ".", "")
			value = strings.ReplaceAll(value, ",", ".")
		} else {
			value = strings.ReplaceAll(value, ",", "")
		}
	case hasComma:
		value = strings.ReplaceAll(value, ",", ".")
	}
	minor, err := money.Parse(value, currency)
	if err != nil || minor <= 0 {
		return 0, false
	}
	return minor, true
}
