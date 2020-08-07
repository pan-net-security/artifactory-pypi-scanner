# Artifactory PyPI Scanner

## Installation

```sh
go get github.com/pan-net-security/artifactory-pypi-scanner
```

## Configuration

`artifactory-pypi-scanner` is configured by environmental variables.

- `ARTIFACTORY_URL`
- `PYPI_URL`
- `PYPI_EMAIL`
- `PYPI_TOKEN`

note: `PYPI_TOKEN` is an API token, not a password. `PYPI_EMAIL` is used for
checking authors of conflicting packages.
