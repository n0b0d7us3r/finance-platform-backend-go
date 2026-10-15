package sms

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// Result is the amount and currency a bank message yields.
// Amount is a precision string: a whole value has no fractional part.
type Result struct {
	Amount   string
	Currency string
	Card     string
}

// Normalize lowercases the text and turns line breaks into spaces.
func Normalize(message string) string {
	message = strings.ReplaceAll(message, "\u00a0", " ")
	message = strings.ReplaceAll(message, "\r\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	return strings.ToLower(strings.TrimSpace(message))
}

// Parse reads an amount from a bank SMS. A stop word yields no result.
// Currency is filled when the text names one.
func Parse(message string, stopWords []string) (Result, bool) {
	text := Normalize(message)
	if stopped(text, stopWords) {
		return Result{}, false
	}
	raw := amountFrom(text)
	if raw == "" {
		return Result{}, false
	}
	amount := normalizeAmount(raw)
	if amount == "" {
		return Result{}, false
	}
	return Result{Amount: amount, Currency: currencyOf(text), Card: cardDigits(text)}, true
}

// Blocked reports a block word and is used to switch the device's requisites off.
func Blocked(message string, words []string) bool {
	text := Normalize(message)
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		pattern := `(?i)(?:^|\s|;)` + regexp.QuoteMeta(word) + `(?:\s|\.|:|,|;|$)`
		if regexp.MustCompile(pattern).MatchString(text) {
			return true
		}
	}
	return false
}

func stopped(text string, words []string) bool {
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		// The empty alternative is intentional: the stored words match inside a token.
		pattern := `(?i)(?:|^|\s|;)` + regexp.QuoteMeta(word) + `(?:\s|\.|:)`
		if regexp.MustCompile(pattern).MatchString(text) {
			return true
		}
	}
	return false
}

var triggers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)чек билайна`),
	regexp.MustCompile(`(?i)перевод\s(?P<amount>\d+(.\d+){0,3})р\sот\s.+\sбаланс`),
	regexp.MustCompile(`(?i)перевод\sна\sсумму\s.+\sиз\s.+\sот\s`),
	regexp.MustCompile(`(?i)perevod\s.+\sot\s.+\siz\s.+\sna\sschet\s`),
	regexp.MustCompile(`(?i)зачислен перевод по`),
	regexp.MustCompile(`(?i)поступление`),
	regexp.MustCompile(`(?i)пополнение`),
	regexp.MustCompile(`(?i)зачисление`),
	regexp.MustCompile(`(?i)зачислено`),
	regexp.MustCompile(`(?i)[а-я]+\sпополнена`),
	regexp.MustCompile(`(?i)popolnenie scheta`),
	regexp.MustCompile(`(?i)postuplenie sredstv na schet`),
	regexp.MustCompile(`(?i)postuplenie`),
	regexp.MustCompile(`(?i)получен перевод`),
	regexp.MustCompile(`(?i)popolnenie`),
	regexp.MustCompile(`(?i)приход на карту`),
	regexp.MustCompile(`(?i)перевод из`),
	regexp.MustCompile(`(?i)vneseno`),
	regexp.MustCompile(`(?i)перевел\(а\) вам`),
	regexp.MustCompile(`(?i)postupil perevod`),
	regexp.MustCompile(`(?i)перевод денежных средств`),
	regexp.MustCompile(`(?i)перевод на карту`),
	regexp.MustCompile(`(?i)zachislenie`),
	regexp.MustCompile(`(?i)^перевод\sот\s`),
	regexp.MustCompile(`(?i)приход`),
	regexp.MustCompile(`(?i)пополнили карту`),
	regexp.MustCompile(`(?i)\sперевод\s.+\sна\sкарту`),
	regexp.MustCompile(`(?i)home\scredit\skazakhstan\sкарточка\s.+\sпополнена\sна\s`),
	regexp.MustCompile(`(?i)\sвы\sполучили\sперевод:\s`),
}

var exceptions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\+\s(?P<amount>\d+(.\d+){0,3})\s₽\.\sтеперь\sна\sкарте\s.+₽$`),
	regexp.MustCompile(`(?i)^\+\s(?P<amount>\d+(.\d+){0,3})\s₽\s-\sбаланс\:\s.+$`),
	regexp.MustCompile(`(?i)^\d{2}\.\d{2}\.\d{2}\s\d{2}\:\d{2}\sзачисление\s\*(?P<card_last_digits>\d{4})\srur\s(?P<amount>\d+(.\d+){0,3})\;\sостаток\s.+$`),
	regexp.MustCompile(`(?i)^\+\s(?P<amount>\d+(.\d+){0,3})\s₽\sот\s.+теперь\sна\sсчете\s.+₽$`),
	regexp.MustCompile(`(?i)^\+\s(?P<amount>\d+(.\d+){0,3})\s₽\s—\sтеперь\sу\sвас\:\s.+$`),
	regexp.MustCompile(`(?i)^\d{2}\:\d{2}\sперевод\s(?P<amount>\d+(.\d+){0,3})р\sна\sкарту\s.+\sбаланс\s.+$`),
	regexp.MustCompile(`(?i)^\+\s(?P<amount>\d+(.\d+){0,3})\s₽\s—\sбаланс\:\s.+$`),
	regexp.MustCompile(`(?i)^совкомбанк\s\+\s(?P<amount>\d+(.\d+){0,3})\s₽\s—\sбаланс\:\s.+(?P<card_last_digits>\d{4})$`),
}

var findAmount = regexp.MustCompile(`(?i)(?:\s|\+)(?P<amount>\d+(.\d+){0,3})\s{0,1}(?:RUB|rub|р|p|₽|RUR|rur|rurcard2card|руб|₸|kzt|TJS|tjs|сом|сомони)(?:\s|\.|,|;|$)`)

var cardPattern = regexp.MustCompile(`(?i)(?:\*|^mir|\smir|счёт|mir-|ecmc|\s••\s|\s\d{6}\.\.|карта\s\*\*\*\s|^карта\s|\s··|\sмир)(?P<card_last_digits>\d{4})(?:\D|$)`)

func amountFrom(text string) string {
	for _, pattern := range exceptions {
		if match := pattern.FindStringSubmatch(text); match != nil {
			if amount := named(pattern, match, "amount"); amount != "" {
				return amount
			}
		}
	}
	for _, pattern := range triggers {
		if pattern.MatchString(text) {
			if match := findAmount.FindStringSubmatch(text); match != nil {
				return named(findAmount, match, "amount")
			}
			return ""
		}
	}
	return ""
}

func named(pattern *regexp.Regexp, match []string, name string) string {
	for i, group := range pattern.SubexpNames() {
		if group == name && i < len(match) {
			return match[i]
		}
	}
	return ""
}

func normalizeAmount(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\u00a0", "")
	raw = strings.ReplaceAll(raw, " ", "")
	switch {
	case strings.Contains(raw, ",") && strings.Contains(raw, "."):
		if strings.LastIndex(raw, ",") > strings.LastIndex(raw, ".") {
			raw = strings.ReplaceAll(raw, ".", "")
			raw = strings.ReplaceAll(raw, ",", ".")
		} else {
			raw = strings.ReplaceAll(raw, ",", "")
		}
	case strings.Contains(raw, ","):
		parts := strings.Split(raw, ",")
		if len(parts) == 2 && utf8.RuneCountInString(parts[1]) <= 2 {
			raw = strings.ReplaceAll(raw, ",", ".")
		} else {
			raw = strings.ReplaceAll(raw, ",", "")
		}
	case strings.Contains(raw, "."):
		parts := strings.Split(raw, ".")
		if !(len(parts) == 2 && utf8.RuneCountInString(parts[1]) <= 2) {
			raw = strings.ReplaceAll(raw, ".", "")
		}
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return ""
	}
	if value.Equal(value.Truncate(0)) {
		return value.Truncate(0).String()
	}
	return value.String()
}

func cardDigits(text string) string {
	match := cardPattern.FindStringSubmatch(text)
	return named(cardPattern, match, "card_last_digits")
}

type currencyRule struct {
	code    string
	pattern *regexp.Regexp
}

var currencies = []currencyRule{
	{"tjs", regexp.MustCompile(`(?i)\btjs\b`)},
	{"tjs", regexp.MustCompile(`(?i)\bсом\b`)},
	{"tjs", regexp.MustCompile(`(?i)\bсомони\b`)},
	{"tjs", regexp.MustCompile(`(?i)\d+\s*сом\b`)},
	{"rub", regexp.MustCompile(`(?i)\brub\b`)},
	{"rub", regexp.MustCompile(`(?i)\bруб\b`)},
	{"rub", regexp.MustCompile(`(?i)\bр\b`)},
	{"rub", regexp.MustCompile(`₽`)},
	{"rub", regexp.MustCompile(`(?i)\bрур\b`)},
	{"rub", regexp.MustCompile(`(?i)rurcard2card`)},
	{"rub", regexp.MustCompile(`(?i)\d+\s*р\b`)},
	{"rub", regexp.MustCompile(`(?i)\d+р\b`)},
	{"rub", regexp.MustCompile(`(?i)\bрублей\b`)},
	{"rub", regexp.MustCompile(`(?i)\bрубля\b`)},
	{"kzt", regexp.MustCompile(`(?i)\bkzt\b`)},
	{"kzt", regexp.MustCompile(`₸`)},
	{"kzt", regexp.MustCompile(`(?i)\bтенге\b`)},
	{"usd", regexp.MustCompile(`(?i)\busd\b`)},
	{"eur", regexp.MustCompile(`(?i)\beur\b`)},
}

func currencyOf(text string) string {
	for _, rule := range currencies {
		if rule.pattern.MatchString(text) {
			return rule.code
		}
	}
	return ""
}
