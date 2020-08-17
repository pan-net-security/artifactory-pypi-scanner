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
- `PYPI_UPLOAD_URL` - used for uploading packages via [legacy](https://warehouse.readthedocs.io/api-reference/legacy/#upload-api) endpoint
- `PYPI_URL` - used for checking presence of package via [json](https://warehouse.readthedocs.io/api-reference/json/#get--pypi--project_name--json) endpoint
- `PYPI_EMAIL` - used for checking authors of conflicting packages
- `PYPI_TOKEN` - API token, not a password

## Example

`artifactory-pypi-scanner` outputs JSON since it is easily consumable by other services.

```
$ artifactory-pypi-scanner | jq
{
  "error": "",
  "artifactoryPackages": 3,
  "pypiPlaceholders": 2,
  "repositoryResults": [
    {
      "error": "",
      "url": "https://af.example.com/artifactory//repository1-pypi-local",
      "packageResults": [
        {
          "error": "",
          "name": "package1",
          "isOurs": false,
          "created": false
        }
      ]
    },
    {
      "error": "",
      "url": "https://af.example.com/artifactory//repository2-pypi-local",
      "packageResults": [
        {
          "error": "",
          "name": "package2",
          "isOurs": true,
          "created": false
        },
        {
          "error": "",
          "name": "package3",
          "isOurs": true,
          "created": true
        }
      ]
    }
  ]
}
```
