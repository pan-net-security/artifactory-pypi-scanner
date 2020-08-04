package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
)

type client struct {
	http               *http.Client
	artifacoryURL      string
	repositoriesURI    string
	pypiPackageInfoURL string
	pypiEmail          string
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

func (c client) handlePackage(packageURL string, done chan<- bool, errChan chan<- error) {
	name, err := c.getPackageNameFromArtifactory(packageURL)
	if err != nil {
		errChan <- fmt.Errorf("failed to get package name: %s", err)
		return
	}
	log.Printf("Found package: %s", name)

	ok, err := c.chechIfPackageIsOurs(name)
	if ok {
		// we own the package, nothig to do
		done <- true
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
		http:               &http.Client{Timeout: time.Second * 10},
		artifacoryURL:      os.Getenv("ARTIFACTORY_URL"),
		repositoriesURI:    "%s/artifactory/api/repositories",
		pypiEmail:          os.Getenv("PYPI_EMAIL"),
		pypiPackageInfoURL: "https://pypi.org/pypi/%s/json",
	}

	if err := client.run(); err != nil {
		log.Fatalln(err)
	}
}
