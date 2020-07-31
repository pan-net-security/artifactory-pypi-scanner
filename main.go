package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/antchfx/xmlquery"
)

const (
	artifacoryURL = "<artifactory-url>"
)

type client struct {
	http            *http.Client
	artifacoryURL   string
	repositoriesURI string
}

type packages []struct {
	// Description string `json:"description"`
	// Key         string `json:"key"`
	// PackageType string `json:"packageType"`
	// Type        string `json:"type"`
	URL string `json:"url"`
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
	req, err := http.NewRequest(
		"GET", fmt.Sprintf(fmt.Sprintf("%s%s/.pypi/simple.html", c.artifacoryURL, url.Path)), nil)
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

	anchor := xmlquery.FindOne(doc, "//a")
	return anchor.InnerText(), nil
}

func (c client) run() error {
	packageURLs, err := c.getPackageURLs()
	if err != nil {
		return fmt.Errorf("failed to get package URLs from AF: %s", err)
	}
	log.Printf("Got %d packeges from artifactory\n", len(packageURLs))

	// TODO: run in parallel
	for _, url := range packageURLs {
		name, err := c.getPackageName(url)
		if err != nil {
			return fmt.Errorf("failed to get package name: %s", err)
		}
		log.Printf("Found package: %s", name)
	}

	return nil
}

func main() {
	client := client{
		http:            &http.Client{Timeout: time.Second * 10},
		artifacoryURL:   artifacoryURL,
		repositoriesURI: "%s/artifactory/api/repositories",
	}

	if err := client.run(); err != nil {
		log.Fatalln(err)
	}
}
