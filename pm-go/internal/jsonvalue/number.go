package jsonvalue

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// number follows the reference serde_json number model: unsigned/signed
// 64-bit integers, then its default significand/exponent float parser. A
// float keeps its type in output even when its value is an integer.
func number(raw string) (json.Number, error) {
	if !strings.ContainsAny(raw, ".eE") && raw != "-0" {
		if strings.HasPrefix(raw, "-") {
			if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
				return json.Number(raw), nil
			}
		} else if _, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return json.Number(raw), nil
		}
	}
	digits := raw
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = digits[1:]
	}
	significand := uint64(0)
	exponent := 0
	i := 0
	overflow := false
	for i < len(digits) && digits[i] >= '0' && digits[i] <= '9' {
		digit := uint64(digits[i] - '0')
		if !overflow && significand <= (math.MaxUint64-digit)/10 {
			significand = significand*10 + digit
		} else {
			overflow = true
			exponent++
		}
		i++
	}
	if i < len(digits) && digits[i] == '.' {
		i++
		overflow = false
		for i < len(digits) && digits[i] >= '0' && digits[i] <= '9' {
			digit := uint64(digits[i] - '0')
			if !overflow && significand <= (math.MaxUint64-digit)/10 {
				significand = significand*10 + digit
				exponent--
			} else {
				overflow = true
			}
			i++
		}
	}
	if i < len(digits) {
		tail := digits[i+1:]
		positive := !strings.HasPrefix(tail, "-")
		tail = strings.TrimLeft(tail, "+-")
		n, err := strconv.ParseUint(tail, 10, 31)
		if err != nil {
			if positive && significand != 0 {
				return "", fmt.Errorf("number out of range")
			}
			return json.Number(floatJSON(math.Copysign(0, sign(negative)))), nil
		}
		if positive {
			exponent += int(n)
		} else {
			exponent -= int(n)
		}
	}
	value := float64(significand)
	for {
		if exponent >= -308 && exponent <= 308 {
			absolute := exponent
			if absolute < 0 {
				absolute = -absolute
			}
			power, _ := strconv.ParseFloat("1e"+strconv.Itoa(absolute), 64)
			if exponent >= 0 {
				value *= power
			} else {
				value /= power
			}
			break
		}
		if value == 0 {
			break
		}
		if exponent >= 0 {
			return "", fmt.Errorf("number out of range")
		}
		value /= 1e308
		exponent += 308
	}
	if math.IsInf(value, 0) {
		return "", fmt.Errorf("number out of range")
	}
	if negative {
		value = -value
	}
	return json.Number(floatJSON(value)), nil
}

func sign(negative bool) float64 {
	if negative {
		return -1
	}
	return 1
}

// zmij's f64 display uses fixed notation for decimal exponents -5..15,
// and a signed exponent with no zero padding everywhere else.
func floatJSON(value float64) string {
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(scientific, "e")
	exponent, _ := strconv.Atoi(exp)
	if exponent >= -5 && exponent <= 15 {
		fixed := strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(fixed, ".") {
			fixed += ".0"
		}
		return fixed
	}
	prefix := "+"
	if exponent < 0 {
		prefix = ""
	}
	return mantissa + "e" + prefix + strconv.Itoa(exponent)
}
