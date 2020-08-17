package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
)

// PackageVersion is default version for python packages
const PackageVersion = "0.0.47"
const setupPy = `from setuptools import setup

setup(name='%s', version='%s', author_email='%s')
`
const pkgInfo = `Metadata-Version: 1.0
Name: %s
Version: %s
Summary: UNKNOWN
Home-page: UNKNOWN
Author: UNKNOWN
Author-email: %s
License: UNKNOWN
Description: UNKNOWN
Platform: UNKNOWN`

type client struct {
	http                 *http.Client
	artifacoryURL        string
	repositoriesURI      string
	pypiPackageInfoURI   string
	pypiPackageUploadURI string
	pypiEmail            string
	pypiToken            string
	pypiURL              string
	pypiUploadURL        string
}

type scannerResult struct {
	Err                 string             `json:"error"`
	ArtifactoryPackages int                `json:"artifactoryPackages"`
	PypiPlaceholders    int                `json:"pypiPlaceholders"`
	RepositoryResults   []repositoryResult `json:"repositoryResults"`
}

type repositoryResult struct {
	Err            string          `json:"error"`
	URL            string          `json:"url"`
	PackageResults []packageResult `json:"packageResults"`
}

type packageResult struct {
	Err     string `json:"error"`
	Name    string `json:"name"`
	IsOurs  bool   `json:"isOurs"`
	Created bool   `json:"created"`
}

type packages []struct {
	URL string `json:"url"`
}

type packageInfo struct {
	Info struct {
		AuthorEmail string `json:"author_email"`
	} `json:"info"`
}

func (c client) getRepositoryURLs() ([]string, error) {
	params := url.Values{}
	params.Add("type", "local")
	params.Add("packageType", "pypi")

	req, err := http.NewRequest("GET", fmt.Sprintf(c.repositoriesURI, c.artifacoryURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %s", err)
	}

	req.URL.RawQuery = params.Encode()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %s", err)
	}
	defer resp.Body.Close()

	packages := packages{}
	if err := json.NewDecoder(resp.Body).Decode(&packages); err != nil {
		return nil, fmt.Errorf("failed to decode response: %s", err)
	}

	var repositoryURLs []string
	for _, item := range packages {
		repositoryURLs = append(repositoryURLs, item.URL)
	}
	return repositoryURLs, nil
}

func (c client) getPackageNamesFromArtifactory(repositoryURL string) ([]string, error) {
	log.Printf("Scanning: %s\n", repositoryURL)
	var packages []string

	url, err := url.Parse(repositoryURL)
	if err != nil {
		return nil, fmt.Errorf("invalid package url: %s", err)
	}

	// FIXME: package does not need to be in artifactory.
	// this is a quick hack since go adds port to url twice
	simpleAPIURL := fmt.Sprintf("%s%s/.pypi/simple.html", c.artifacoryURL, url.Path)
	req, err := http.NewRequest("GET", fmt.Sprintf(simpleAPIURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %s", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %s", err)
	}
	defer resp.Body.Close()

	doc, err := xmlquery.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	for _, anchor := range xmlquery.Find(doc, "//a") {
		text := anchor.InnerText()
		requires := anchor.SelectAttr("data-requires-python")
		// artifactory has a bug when it leaks anchor attributes to the inner
		// text. this filters out anchors with malformed inner texts
		if requires == "" || !strings.Contains(text, requires[1:]) {
			packages = append(packages, text)
		}
	}

	if len(packages) == 0 {
		return nil, fmt.Errorf("no package found at %s", simpleAPIURL)
	}

	return packages, nil
}

func (c client) chechIfPackageIsOurs(packageName string) (bool, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(c.pypiPackageInfoURI, c.pypiURL, packageName), nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %s", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to get %s from PyPI: %s", packageName, err)
	}
	defer resp.Body.Close()

	packageInfo := packageInfo{}
	if err := json.NewDecoder(resp.Body).Decode(&packageInfo); err != nil {
		return false, fmt.Errorf("failed to decode response from PyPI: %s", err)
	}

	log.Printf("%s has '%s' as author", packageName, packageInfo.Info.AuthorEmail)
	return strings.EqualFold(packageInfo.Info.AuthorEmail, c.pypiEmail), nil
}

func (c client) createPackage(name string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tarball := tar.NewWriter(gz)

	pgkInfoContent := []byte(fmt.Sprintf(pkgInfo, name, PackageVersion, c.pypiEmail))
	setupPyContent := []byte(fmt.Sprintf(setupPy, name, PackageVersion, c.pypiEmail))

	files := map[string][]byte{
		fmt.Sprintf("%s-%s/PKG-INFO", name, PackageVersion):                   pgkInfoContent,
		fmt.Sprintf("%s-%s/setup.py", name, PackageVersion):                   setupPyContent,
		fmt.Sprintf("%s-%s/%s.egg-info/PKG-INFO", name, PackageVersion, name): pgkInfoContent,
	}

	for name, content := range files {
		header := tar.Header{
			Name:     name,
			Mode:     0600,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tarball.WriteHeader(&header); err != nil {
			tarball.Close()
			gz.Close()
			return nil, fmt.Errorf("unable to tar header of %s: %s", name, err)
		}
		if _, err := tarball.Write(content); err != nil {
			tarball.Close()
			gz.Close()
			return nil, fmt.Errorf("unable to tar content of %s: %s", name, err)
		}

	}

	if err := tarball.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c client) uploadPackage(name string) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	defer writer.Close()

	fileWritter, err := writer.CreateFormFile("content", fmt.Sprintf("%s-%s.tar.gz", name, PackageVersion))
	if err != nil {
		return fmt.Errorf("failed to add dist to request: %s", err)
	}

	packageTar, err := c.createPackage(name)
	if err != nil {
		return fmt.Errorf("unable to create %s.tar.gz: %s", name, err)
	}

	if _, err := io.Copy(fileWritter, bytes.NewReader(packageTar)); err != nil {
		return fmt.Errorf("unable to copy package tar multipart writter")
	}

	for fieldName, fieldValue := range map[string]string{
		":action":          "file_upload",
		"protocol_version": "1",
		"filetype":         "sdist",
		"pyversion":        "source",
		"name":             name,
		"metadata_version": "1.0",
		"author_email":     c.pypiEmail,
		"version":          PackageVersion,
		"md5_digest":       fmt.Sprintf("%x", md5.Sum(packageTar)),
	} {
		field, err := writer.CreateFormField(fieldName)
		if err != nil {
			return fmt.Errorf("failed to create form field for %s: %s", fieldName, err)
		}
		if _, err := field.Write([]byte(fieldValue)); err != nil {
			return fmt.Errorf("failed to write field %s: %s", fieldName, err)
		}
	}

	uploadURL := fmt.Sprintf(c.pypiPackageUploadURI, c.pypiUploadURL)
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request to %s: %s", uploadURL, err)
	}

	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.SetBasicAuth("__token__", c.pypiToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create PyPI package: %s", err)
	}

	if resp.StatusCode != 200 {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			body = []byte("[none]")
		}
		return fmt.Errorf("Invalid response from PyPI received: %d. Body: %s", resp.StatusCode, body)
	}
	return nil
}

func (c client) handlePackage(name string, resultChan chan<- packageResult) {
	result := packageResult{Name: name}

	isOurs, err := c.chechIfPackageIsOurs(name)
	result.IsOurs = isOurs
	if err == nil {
		// this means that package exists at PyPI, we just report the result
		resultChan <- result
		return
	}

	log.Printf("Received error from PyPI: %s. Trying to create %s package.\n", err, name)
	if err := c.uploadPackage(name); err != nil {
		result.Err = fmt.Sprintf("unable to create package '%s': %s", name, err)
		resultChan <- result
		return
	}
	result.Created = true
	result.IsOurs = true
	resultChan <- result
}

func (c client) handleRepository(repositoryURL string, resultChan chan<- repositoryResult) {
	log.Printf("Handling %s repository\n", repositoryURL)
	result := repositoryResult{URL: repositoryURL}
	names, err := c.getPackageNamesFromArtifactory(repositoryURL)
	if err != nil {
		result.Err = fmt.Sprintf("failed to get package name: %s", err)
		resultChan <- result
		return
	}

	packageResultChan := make(chan packageResult)
	for _, name := range names {
		log.Printf("Handling package: %s", name)
		go c.handlePackage(name, packageResultChan)
	}

	for i := 0; i < len(names); i++ {
		select {
		case packageResult := <-packageResultChan:
			result.PackageResults = append(result.PackageResults, packageResult)
			log.Printf("Got result from package: %s", packageResult.Name)
		}
	}
	resultChan <- result
}

func (c client) run() error {
	scannerResult := scannerResult{}

	repositoryURLs, err := c.getRepositoryURLs()
	if err != nil {
		return fmt.Errorf("failed to get package URLs from AF: %s", err)
	}

	log.Printf("Got %d repositories from artifactory\n", len(repositoryURLs))

	resultChan := make(chan repositoryResult)
	for _, url := range repositoryURLs {
		go c.handleRepository(url, resultChan)
	}

	for i := 0; i < len(repositoryURLs); i++ {
		select {
		case result := <-resultChan:
			scannerResult.RepositoryResults = append(scannerResult.RepositoryResults, result)
			if result.Err != "" {
				log.Printf("Error handling %s: %s\n", result.URL, result.Err)
			}
			log.Printf("Successfully handled %s repository\n", result.URL)
			for _, r := range result.PackageResults {
				scannerResult.ArtifactoryPackages++
				if r.Created || r.IsOurs {
					scannerResult.PypiPlaceholders++
				}
			}
		}
	}

	jsonResult, err := json.Marshal(scannerResult)
	if err != nil {
		return fmt.Errorf("unable to marshall scanner result: %s", err)
	}
	fmt.Printf("%s\n", jsonResult)
	return nil
}

func main() {
	client := client{
		http:                 &http.Client{Timeout: time.Second * 10},
		artifacoryURL:        os.Getenv("ARTIFACTORY_URL"),
		repositoriesURI:      "%s/artifactory/api/repositories",
		pypiURL:              os.Getenv("PYPI_URL"),
		pypiUploadURL:        os.Getenv("PYPI_UPLOAD_URL"),
		pypiEmail:            os.Getenv("PYPI_EMAIL"),
		pypiToken:            os.Getenv("PYPI_TOKEN"),
		pypiPackageInfoURI:   "%s/pypi/%s/json",
		pypiPackageUploadURI: "%s/legacy/",
	}

	if err := client.run(); err != nil {
		log.Fatalln(err)
	}
}
