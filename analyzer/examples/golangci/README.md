# golangci-lint module adapter

Keep the adapter in a separate module so `testinputs` stays independent of host
versions. Follow the host's [module-plugin documentation](https://golangci-lint.run/docs/plugins/module-plugins/).
The adapter needs `testinputs` and `github.com/golangci/plugin-module-register/register`.
Require `github.com/lukasschwab/testinputs` at the version used by the consuming
repository. For local development, a `replace` directive can point to the checkout
path. Pin the registry and host versions according to the consuming repository.

```go
package testinputsplugin

import (
    "github.com/lukasschwab/testinputs/analyzer"

    "github.com/golangci/plugin-module-register/register"
    "golang.org/x/tools/go/analysis"
)

func init() {
    register.Plugin("testinputs", func(any) (register.LinterPlugin, error) {
        return plugin{}, nil
    })
}

type plugin struct{}

func (plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
    return []*analysis.Analyzer{analyzer.New()}, nil
}

func (plugin) GetLoadMode() string {
    return register.LoadModeTypesInfo
}
```

Declare the adapter's module/path under `plugins` in `.custom-gcl.yml`, select
the desired golangci-lint version, then build with `golangci-lint custom`.
Enable the registered analyzer in the consuming repository:

```yaml
version: "2"
linters:
  enable:
    - testinputs
  settings:
    custom:
      testinputs:
        type: module
        description: Potential runtime filesystem dependencies in tests
```

The host loads type information and handles `//nolint:testinputs`. For example:

```go
checkSourceContract("../api/schema.go") //nolint:testinputs // Verify the checked-in source contract.
```

The raw analyzer still emits a finding at that call. No application linter
configuration is changed by building or running testinputs. This adapter is an
integration example; the test suite verifies the standard Go driver, not a
particular golangci-lint distribution.
