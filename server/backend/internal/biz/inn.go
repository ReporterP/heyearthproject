package biz

import (
	"errors"
	"strconv"
)

// ValidateINN проверяет корректность ИНН (10 или 12 цифр + контрольная сумма).
// Формат согласно приказу ФНС ММ-3-09/11.
func ValidateINN(inn string) error {
	if len(inn) != 10 && len(inn) != 12 {
		return errors.New("ИНН должен содержать 10 (юрлицо) или 12 (физлицо/ИП) цифр")
	}
	digits := make([]int, len(inn))
	for i, c := range inn {
		if c < '0' || c > '9' {
			return errors.New("ИНН должен содержать только цифры")
		}
		digits[i], _ = strconv.Atoi(string(c))
	}

	if len(inn) == 10 {
		// Контрольная цифра: pos 10
		coef := []int{2, 4, 10, 3, 5, 9, 4, 6, 8}
		sum := 0
		for i, c := range coef {
			sum += c * digits[i]
		}
		check := (sum % 11) % 10
		if check != digits[9] {
			return errors.New("неверная контрольная сумма ИНН")
		}
	} else {
		// 12 цифр: две контрольные
		coef1 := []int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
		sum1 := 0
		for i, c := range coef1 {
			sum1 += c * digits[i]
		}
		check1 := (sum1 % 11) % 10
		if check1 != digits[10] {
			return errors.New("неверная контрольная сумма ИНН (цифра 11)")
		}
		coef2 := []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
		sum2 := 0
		for i, c := range coef2 {
			sum2 += c * digits[i]
		}
		check2 := (sum2 % 11) % 10
		if check2 != digits[11] {
			return errors.New("неверная контрольная сумма ИНН (цифра 12)")
		}
	}
	return nil
}
