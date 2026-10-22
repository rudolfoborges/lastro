package taxonomy

type Category struct {
	Code  string
	Label string
}

const Unidentified = "unidentified"

var Defaults = []Category{
	{Code: "groceries", Label: "Mercado"},
	{Code: "meals", Label: "Alimentação e refeições"},
	{Code: "convenience_snacks", Label: "Conveniência — bebidas e snacks"},
	{Code: "fuel", Label: "Combustível"},
	{Code: "transport", Label: "Transporte"},
	{Code: "home", Label: "Casa e reforma"},
	{Code: "health", Label: "Saúde"},
	{Code: "education", Label: "Educação"},
	{Code: "clothing", Label: "Vestuário"},
	{Code: "electronics", Label: "Eletrônicos"},
	{Code: "software_subscriptions", Label: "Software e assinaturas"},
	{Code: "leisure", Label: "Lazer"},
	{Code: "financial_charges", Label: "Encargos financeiros"},
	{Code: Unidentified, Label: "Não identificado"},
}
