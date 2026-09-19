# golangci-lint module adapter

Keep the adapter in a separate module so `testfs` stays independent of host
versions. Follow the host's [module-plugin documentation](https://golangci-lint.run/docs/plugins/module-plugins/).
The adapter needs `testfs` and `github.com/golangci/plugin-module-register/register`.
For this unpublished local module, use `require testfs v0.0.0` and a `replace`
directive pointing to the absolute checkout path. Pin the registry and host
versions according to the consuming repository.

```go
package testfsplugin

import (
    "testfs"

    "github.com/golangci/plugin-module-register/register"
    "golang.org/x/tools/go/analysis"
)

func init() {
    register.Plugin("testfs", func(any) (register.LinterPlugin, error) {
        return plugin{}, nil
    })
}

type plugin struct{}

func (plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
    return []*analysis.Analyzer{testfs.New()}, nil
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
    - testfs
  settings:
    custom:
      testfs:
        type: module
        description: Potential runtime filesystem dependencies in tests
```

The host loads type information and handles `//nolint:testfs`. For example:

```go
checkSourceContract("../api/schema.go") //nolint:testfs // Verify the checked-in source contract.
```

The raw analyzer still emits a finding at that call. No application linter
configuration is changed by building or running testfs. This adapter is an
integration example; the test suite verifies the standard Go driver, not a
particular golangci-lint distribution.
