package web

import (
	"hash/fnv"
	"strings"
)

type categoryStyle struct {
	Class string
	Color string
}

type categoryPaletteEntry struct {
	class string
	color string
}

var categoryPalette = []categoryPaletteEntry{
	{class: "cat-palette-1", color: "#38bdf8"},
	{class: "cat-palette-2", color: "#a78bfa"},
	{class: "cat-palette-3", color: "#f472b6"},
	{class: "cat-palette-4", color: "#fbbf24"},
	{class: "cat-palette-5", color: "#34d399"},
	{class: "cat-palette-6", color: "#fb7185"},
	{class: "cat-palette-7", color: "#60a5fa"},
	{class: "cat-palette-8", color: "#c084fc"},
}

var knownCategoryStyles = []struct {
	names []string
	style categoryStyle
}{
	{names: []string{"groceries", "grocery", "supermarket", "supermercado"}, style: categoryStyle{Class: "cat-groceries", Color: "#34d399"}},
	{names: []string{"utilities", "utility", "electricity", "gas", "water", "telecom"}, style: categoryStyle{Class: "cat-utilities", Color: "#fbbf24"}},
	{names: []string{"restaurants", "restaurant", "dining", "meals", "bars", "cafes", "coffee"}, style: categoryStyle{Class: "cat-dining", Color: "#a78bfa"}},
	{names: []string{"software", "tech", "technology", "subscriptions", "subscription"}, style: categoryStyle{Class: "cat-software", Color: "#38bdf8"}},
	{names: []string{"rent", "housing", "mortgage"}, style: categoryStyle{Class: "cat-housing", Color: "#60a5fa"}},
	{names: []string{"health", "healthcare", "medical", "pharmacy"}, style: categoryStyle{Class: "cat-health", Color: "#fb7185"}},
	{names: []string{"shopping", "shops"}, style: categoryStyle{Class: "cat-shopping", Color: "#f472b6"}},
	{names: []string{"travel", "transport", "transportation", "taxi", "uber"}, style: categoryStyle{Class: "cat-travel", Color: "#fb923c"}},
	{names: []string{"insurance"}, style: categoryStyle{Class: "cat-insurance", Color: "#2dd4bf"}},
	{names: []string{"bank fees", "bank fee", "fees", "taxes", "tax"}, style: categoryStyle{Class: "cat-fees", Color: "#f87171"}},
	{names: []string{"payroll", "salary", "income", "sales", "revenue"}, style: categoryStyle{Class: "cat-income", Color: "#4ade80"}},
	{names: []string{"professional services", "professional service", "services", "accounting", "office"}, style: categoryStyle{Class: "cat-services", Color: "#818cf8"}},
	{names: []string{"entertainment", "ocio", "leisure"}, style: categoryStyle{Class: "cat-entertainment", Color: "#e879f9"}},
	{names: []string{"emergency fund"}, style: categoryStyle{Class: "cat-emergency", Color: "#2dd4bf"}},
	{names: []string{"investments", "investment"}, style: categoryStyle{Class: "cat-investments", Color: "#22d3ee"}},
	{names: []string{"savings", "saving"}, style: categoryStyle{Class: "cat-savings", Color: "#14b8a6"}},
	{names: []string{"transfers / bizum", "bizum", "transfers / p2p", "p2p"}, style: categoryStyle{Class: "cat-bizum", Color: "#c084fc"}},
	{names: []string{"transfer", "transfers"}, style: categoryStyle{Class: "cat-transfer", Color: "#64748b"}},
	{names: []string{"uncategorized", "uncategorised", ""}, style: categoryStyle{Class: "cat-uncategorized", Color: "#94a3b8"}},
}

func categoryStyleFor(category string) categoryStyle {
	normalized := strings.ToLower(strings.TrimSpace(category))
	for _, entry := range knownCategoryStyles {
		for _, name := range entry.names {
			if normalized == name {
				return entry.style
			}
		}
	}
	for _, entry := range knownCategoryStyles {
		for _, name := range entry.names {
			if name != "" && strings.Contains(normalized, name) {
				return entry.style
			}
		}
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(normalized))
	entry := categoryPalette[int(hash.Sum32())%len(categoryPalette)]
	return categoryStyle{Class: entry.class, Color: entry.color}
}

func categoryClass(category string) string {
	return categoryStyleFor(category).Class
}

func categoryColor(category string) string {
	return categoryStyleFor(category).Color
}
