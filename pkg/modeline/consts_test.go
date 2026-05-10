package modeline

// Shared test fixture literals. Hoisted into package-level consts to keep
// goconst quiet across modeline_test.go and contract_test.go.
const (
	testNodeIP1            = "1.2.3.4"
	testNodeIP2            = "192.168.100.2"
	testLoopback           = "127.0.0.1"
	testTemplateControlPln = "templates/controlplane.yaml"
)
