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
const PackageVersion = "0.0.0"
const setupPy = `from setuptools import setup

setup(name='%s', version='%s')
`
const pkgInfo = `Metadata-Version: 1.0
Name: %s
Version: %s
Summary: UNKNOWN
Home-page: UNKNOWN
Author: UNKNOWN
Author-email: UNKNOWN
License: UNKNOWN
Description: UNKNOWN
Platform: UNKNOWN`

type client struct {
	http                 *http.Client
	artifacoryURL        string
	repositoriesURI      string
	pypiPackageInfoURL   string
	pypiPackageUploadURL string
	pypiEmail            string
	pypiToken            string
}

type packages []struct {
	// Description string `json:"description"`
	// Key         string `json:"key"`
	// PackageType string `json:"packageType"`
	// Type        string `json:"type"`
	URL string `json:"url"`
}

type packageInfo struct {
	Info struct {
		Author      string `json:"author"`
		AuthorEmail string `json:"author_email"`
	} `json:"info"`
}

func (c client) getPackageURLs() ([]string, error) {
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

	var packageURLs []string
	for _, item := range packages {
		packageURLs = append(packageURLs, item.URL)
	}
	return packageURLs, nil
}

func (c client) getPackageNameFromArtifactory(packageURL string) (string, error) {
	log.Printf("Scanning: %s\n", packageURL)

	url, err := url.Parse(packageURL)
	if err != nil {
		return "", fmt.Errorf("invalid package url: %s", err)
	}

	// FIXME: package does not need to be in artifactory.
	// this is a quick hack since go adds port to url twice
	simpleAPIURL := fmt.Sprintf("%s%s/.pypi/simple.html", c.artifacoryURL, url.Path)
	req, err := http.NewRequest("GET", fmt.Sprintf(simpleAPIURL), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %s", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %s", err)
	}
	defer resp.Body.Close()

	doc, err := xmlquery.Parse(resp.Body)
	if err != nil {
		return "", err
	}

	for _, anchor := range xmlquery.Find(doc, "//a") {
		text := anchor.InnerText()
		requires := anchor.SelectAttr("data-requires-python")
		// artifactory has a bug when it leaks anchor attributes to the inner
		// text. this filters out anchors with malformed inner texts
		if requires == "" || !strings.Contains(text, requires[1:]) {
			return text, nil
		}
	}
	return "", fmt.Errorf("no package found at %s", simpleAPIURL)
}

func (c client) chechIfPackageIsOurs(packageName string) (bool, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(c.pypiPackageInfoURL, packageName), nil)
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

	return strings.EqualFold(packageInfo.Info.AuthorEmail, c.pypiEmail), nil
}

func createPackage(name string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tarball := tar.NewWriter(gz)

	files := map[string][]byte{
		fmt.Sprintf("%s-%s/PKG-INFO", name, PackageVersion): []byte(fmt.Sprintf(pkgInfo, name, PackageVersion)),
		fmt.Sprintf("%s-%s/setup.py", name, PackageVersion): []byte(fmt.Sprintf(setupPy, name, PackageVersion)),
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

	packageTar, err := createPackage(name)
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

	req, err := http.NewRequest("POST", c.pypiPackageUploadURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request to %s: %s", c.pypiPackageUploadURL, err)
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

func (c client) handlePackage(packageURL string, done chan<- bool, errChan chan<- error) {
	name, err := c.getPackageNameFromArtifactory(packageURL)
	if err != nil {
		errChan <- fmt.Errorf("failed to get package name: %s", err)
		return
	}
	log.Printf("Found package: %s", name)

	isOurs, err := c.chechIfPackageIsOurs(name)
	if isOurs {
		// We own the package, nothing to do
		done <- true
		return
	}

	if err == nil && !isOurs {
		// Oh no, maybe malicious package
		errChan <- fmt.Errorf("package with unknown owner")
		return
	}

	if err := c.uploadPackage(name); err != nil {
		errChan <- fmt.Errorf("unable to create package '%s': %s", name, err)
		return
	}

	done <- true
}

func (c client) run() error {
	packageURLs, err := c.getPackageURLs()
	if err != nil {
		return fmt.Errorf("failed to get package URLs from AF: %s", err)
	}
	log.Printf("Got %d packeges from artifactory\n", len(packageURLs))

	errChan := make(chan error)
	doneChan := make(chan bool)
	for _, url := range packageURLs {
		go c.handlePackage(url, doneChan, errChan)
	}

	for i := 0; i < len(packageURLs); i++ {
		select {
		case <-doneChan:
		case err := <-errChan:
			log.Println(err)
		}
	}
	return nil
}

func main() {
	client := client{
		http:                 &http.Client{Timeout: time.Second * 10},
		artifacoryURL:        os.Getenv("ARTIFACTORY_URL"),
		repositoriesURI:      "%s/artifactory/api/repositories",
		pypiEmail:            os.Getenv("PYPI_EMAIL"),
		pypiPackageInfoURL:   "https://pypi.org/pypi/%s/json",
		pypiPackageUploadURL: os.Getenv("PYPI_URL"),
		pypiToken:            os.Getenv("PYPI_TOKEN"),
	}

	if err := client.run(); err != nil {
		log.Fatalln(err)
	}
}
