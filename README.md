# Artifactory PyPI Scanner

`artifactory-pypi-scanner` lists all Python packages in a [jfrog artifactory](https://jfrog.com/artifactory/) instance. Checks whether they're present on [PyPI](https://pypi.org/) and creates new ones with the same name if they do not exist on PyPI.

## But why?

When `pip` searches for packages, it searches PyPI first and other repositories next. This creates a security issue which was described [here](https://github.com/pypa/pip/issues/8606). Let's say we have a package in an internal repository. Someone can upload a package containing malicious code with the same name to PyPI. When downloading the package users will download the malicious package. To prevent this package injection we create a placeholder on PyPI.

> Be nice and prefix your package name with, e.g. name of your organization, not to clutter PyPI


## Installation

```sh
go get github.com/pan-net-security/artifactory-pypi-scanner
```

## Configuration

`artifactory-pypi-scanner` is configured by environmental variables. All variables are mandatory.

- `ARTIFACTORY_URL`
- `PYPI_URL`
- `PYPI_EMAIL` - used for checking authors of conflicting packages
- `PYPI_TOKEN` - API token, not a password

## Example

`artifactory-pypi-scanener` outputs JSON since it is easily consumable by other services.

```
$ artifactory-pypi-scanner | jq
{
  "error": "",
  "artifactoryPackages": 4,
  "pypiPlaceholders": 3,
  "ignoredPackages": 0,
  "packageResults": [
    {
      "error": "",
      "name": "package1",
      "url": "https://af.example.com/artifactory//package1-pypi-local",
      "isOurs": false,
      "created": false
    },
    {
      "error": "",
      "name": "package2",
      "url": "https://af.example.com/artifactory//package2-pypi-local",
      "isOurs": true,
      "created": false
    },
    {
      "error": "",
      "name": "package3",
      "url": "https://af.example.com/artifactory//package3-pypi-local",
      "isOurs": true,
      "created": true
    },
    {
      "error": "",
      "name": "package4",
      "url": "https://af.example.com/artifactory//package4-pypi-local",
      "isOurs": true,
      "created": true
    }
  ]
}
```
