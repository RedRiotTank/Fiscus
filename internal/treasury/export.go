package treasury

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// ExportFormat identifies a supported accounting export.
type ExportFormat string

const (
	// FormatCSV is a tabular transaction export.
	FormatCSV ExportFormat = "csv"
	// FormatHLedger is a hledger-compatible journal.
	FormatHLedger ExportFormat = "hledger"
	// FormatBeancount is a Beancount ledger.
	FormatBeancount ExportFormat = "beancount"
)

// ParseFormat normalizes and validates an export format.
func ParseFormat(raw string) (ExportFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "csv":
		return FormatCSV, nil
	case "hledger", "journal":
		return FormatHLedger, nil
	case "beancount":
		return FormatBeancount, nil
	default:
		return "", fmt.Errorf("unsupported export format %q (use csv, hledger, or beancount)", raw)
	}
}

// Export renders transactions in the requested format.
func Export(format ExportFormat, transactions []domain.Transaction, accounts []domain.Account) ([]byte, error) {
	accountByID := make(map[string]domain.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	ordered := append([]domain.Transaction(nil), transactions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].BookingDate.Equal(ordered[j].BookingDate) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].BookingDate.Before(ordered[j].BookingDate)
	})
	switch format {
	case FormatCSV:
		return exportCSV(ordered, accountByID)
	case FormatHLedger:
		return exportHLedger(ordered, accountByID), nil
	case FormatBeancount:
		return exportBeancount(ordered, accountByID), nil
	default:
		return nil, errors.New("treasury: unsupported export format")
	}
}

// Extension returns the conventional file extension for a format.
func Extension(format ExportFormat) string {
	switch format {
	case FormatHLedger:
		return ".journal"
	case FormatBeancount:
		return ".beancount"
	default:
		return ".csv"
	}
}

// ContentType returns the HTTP content type for a format.
func ContentType(format ExportFormat) string {
	switch format {
	case FormatHLedger, FormatBeancount:
		return "text/plain; charset=utf-8"
	default:
		return "text/csv; charset=utf-8"
	}
}

func exportCSV(transactions []domain.Transaction, accounts map[string]domain.Account) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"date", "account", "iban", "counterparty", "description", "category",
		"amount", "currency", "status",
	}); err != nil {
		return nil, err
	}
	for _, tx := range transactions {
		account := accounts[tx.AccountID]
		if err := writer.Write([]string{
			tx.BookingDate.UTC().Format("2006-01-02"),
			accountLabel(tx.AccountID, account),
			account.IBAN,
			tx.Counterparty,
			tx.Description,
			categoryName(tx.Category),
			money.Format(tx.AmountMinor, tx.Currency),
			strings.ToUpper(tx.Currency),
			tx.Status,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func exportHLedger(transactions []domain.Transaction, accounts map[string]domain.Account) []byte {
	var buffer bytes.Buffer
	buffer.WriteString("; Fiscus hledger journal\n\n")
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		account := hledgerAccount(tx.AccountID, accounts)
		category := hledgerCategory(tx.Category, tx.AmountMinor >= 0)
		currency := strings.ToUpper(tx.Currency)
		payee := cleanText(tx.Counterparty)
		if payee == "" {
			payee = cleanText(tx.Description)
		}
		if payee == "" {
			payee = "Bank transaction"
		}
		description := cleanText(tx.Description)
		if description == "" {
			description = payee
		}
		fmt.Fprintf(&buffer, "%s * %s | %s\n", tx.BookingDate.UTC().Format("2006-01-02"), payee, description)
		if tx.AmountMinor < 0 {
			fmt.Fprintf(&buffer, "    %s  %s %s\n", category, money.Format(-tx.AmountMinor, currency), currency)
			fmt.Fprintf(&buffer, "    %s  %s %s\n\n", account, money.Format(tx.AmountMinor, currency), currency)
		} else {
			fmt.Fprintf(&buffer, "    %s  %s %s\n", account, money.Format(tx.AmountMinor, currency), currency)
			fmt.Fprintf(&buffer, "    %s  %s %s\n\n", category, money.Format(-tx.AmountMinor, currency), currency)
		}
	}
	return buffer.Bytes()
}

func exportBeancount(transactions []domain.Transaction, accounts map[string]domain.Account) []byte {
	var buffer bytes.Buffer
	buffer.WriteString("; Fiscus Beancount export\n\n")
	open := beancountOpenDirectives(transactions, accounts)
	for _, directive := range open {
		buffer.WriteString(directive)
	}
	if len(open) > 0 {
		buffer.WriteString("\n")
	}
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		account := beancountAccount(tx.AccountID, accounts, "Assets")
		category := beancountCategory(tx.Category, tx.AmountMinor >= 0)
		currency := strings.ToUpper(tx.Currency)
		payee := cleanText(tx.Counterparty)
		if payee == "" {
			payee = cleanText(tx.Description)
		}
		if payee == "" {
			payee = "Bank transaction"
		}
		description := cleanText(tx.Description)
		if description == "" {
			description = payee
		}
		fmt.Fprintf(&buffer, "%s * %q %q\n", tx.BookingDate.UTC().Format("2006-01-02"), payee, description)
		if tx.AmountMinor < 0 {
			fmt.Fprintf(&buffer, "  %s  %s %s\n", category, money.Format(-tx.AmountMinor, currency), currency)
			fmt.Fprintf(&buffer, "  %s  %s %s\n\n", account, money.Format(tx.AmountMinor, currency), currency)
		} else {
			fmt.Fprintf(&buffer, "  %s  %s %s\n", account, money.Format(tx.AmountMinor, currency), currency)
			fmt.Fprintf(&buffer, "  %s  %s %s\n\n", category, money.Format(-tx.AmountMinor, currency), currency)
		}
	}
	return buffer.Bytes()
}

func beancountOpenDirectives(transactions []domain.Transaction, accounts map[string]domain.Account) []string {
	used := make(map[string]map[string]struct{})
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		currency := strings.ToUpper(tx.Currency)
		account := beancountAccount(tx.AccountID, accounts, "Assets")
		category := beancountCategory(tx.Category, tx.AmountMinor >= 0)
		for _, name := range []string{account, category} {
			if used[name] == nil {
				used[name] = make(map[string]struct{})
			}
			used[name][currency] = struct{}{}
		}
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		currencies := make([]string, 0, len(used[name]))
		for currency := range used[name] {
			currencies = append(currencies, currency)
		}
		sort.Strings(currencies)
		out = append(out, fmt.Sprintf("2000-01-01 open %s %s\n", name, strings.Join(currencies, ",")))
	}
	return out
}

func hledgerAccount(accountID string, accounts map[string]domain.Account) string {
	account := accounts[accountID]
	name := account.Name
	if name == "" {
		name = account.IBAN
	}
	if name == "" {
		name = accountID
	}
	return "assets:bank:" + slug(name)
}

func hledgerCategory(category string, income bool) string {
	if strings.TrimSpace(category) == "" {
		category = "Uncategorized"
	}
	root := "expenses"
	if income {
		root = "income"
	}
	return root + ":" + slug(category)
}

func beancountAccount(accountID string, accounts map[string]domain.Account, root string) string {
	account := accounts[accountID]
	name := account.Name
	if name == "" {
		name = account.IBAN
	}
	if name == "" {
		name = accountID
	}
	return root + ":Bank:" + titleComponent(name)
}

func beancountCategory(category string, income bool) string {
	if strings.TrimSpace(category) == "" {
		category = "Uncategorized"
	}
	root := "Expenses"
	if income {
		root = "Income"
	}
	return root + ":" + titleComponent(category)
}

func accountLabel(accountID string, account domain.Account) string {
	if account.Name != "" {
		return account.Name
	}
	if account.IBAN != "" {
		return account.IBAN
	}
	return accountID
}

func categoryName(category string) string {
	if strings.TrimSpace(category) == "" {
		return "Uncategorized"
	}
	return category
}

func cleanText(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

func slug(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func titleComponent(value string) string {
	slugged := slug(value)
	parts := strings.Split(slugged, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		parts[i] = string(unicode.ToUpper(runes[0])) + string(runes[1:])
	}
	return strings.Join(parts, "")
}
