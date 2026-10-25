// Package acceptance holds the end-to-end tests of the PRD acceptance
// criteria (§12) and the list the CI report links them to.
package acceptance

import (
	"fmt"
	"regexp"
	"strconv"
)

type Criterion struct {
	Number int
	Title  string
}

// Criteria are the PRD §12 acceptance criteria; titles are pt-BR, as in the PRD.
var Criteria = []Criterion{
	{1, "Registro por conversa"},
	{2, "Repetição de notificação"},
	{3, "Mesma compra em três fontes"},
	{4, "Compras iguais legítimas"},
	{5, "Compra parcelada"},
	{6, "Pix com juros"},
	{7, "Finalidade desconhecida"},
	{8, "Conveniência"},
	{9, "Fatura repetida"},
	{10, "Estorno parcial"},
	{11, "Cobertura incompleta"},
	{12, "Fatura conferida"},
	{13, "Correção preservada"},
	{14, "Privacidade"},
}

var testName = regexp.MustCompile(`^TestCriterion(\d{2})[A-Z]\w*$`)

// NumberOf returns the criterion a top-level test covers, from its name.
func NumberOf(test string) (int, bool) {
	match := testName.FindStringSubmatch(test)
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

func TestPrefix(number int) string {
	return fmt.Sprintf("TestCriterion%02d", number)
}
