package telemetry

//go:generate go tool moq -out moq_test.go -pkg telemetry ../llm Provider
//go:generate go tool moq -out toolset_moq_test.go -pkg telemetry ../agent Toolset
