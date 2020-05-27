package main

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fastjson"
)

const ApiApp = "/api/app"
const ApiUserLogin = "/api/user/login"
const ApiAppTagList = "/api/tag/list"
const ApiAppDocument = "/api/document"
const ApiAppDocumentList = "/api/document/list"
const ApiAppFile = "/api/file"

var Language string
var TagNames []string
var Host string
var Path string

var Tags []string

func init() {
	TagNames = strings.Split(os.Getenv("TAGS"), ",")
	Language = os.Getenv("LANG")
	Host = os.Getenv("HOST")
	Path = os.Getenv("IMPORT_PATH")
}

func main() {
	client := resty.New()
	client.SetHostURL(Host)

	CheckServer(client)

	Login(client)

	tagData := GetTags(client)
	for _, name := range TagNames {
		for _, tagEntry := range tagData.GetArray("tags") {
			if !bytes.Equal([]byte(name), tagEntry.GetStringBytes("name")) {
				continue
			}

			Tags = append(Tags, string(tagEntry.GetStringBytes("id")))
		}
	}

	sort.Strings(Tags)

	files := GatherFiles()

	documents := GetDocuments(client)

	for _, filePath := range files {
		name := getFilename(filePath)

		var documentID string
		var hasFile bool
		for _, doc := range documents.GetArray("documents") {
			docTitle := string(doc.GetStringBytes("title"))
			if name != docTitle {
				continue
			}

			var tags []string
			for _, tag := range doc.GetArray("tags") {
				tags = append(tags, string(tag.GetStringBytes("id")))
			}

			sort.Strings(tags)

			if !reflect.DeepEqual(tags, Tags) {
				continue
			}

			documentID = string(doc.GetStringBytes("id"))

			if doc.GetInt("file_count") > 0 {
				hasFile = true
			}
		}

		if len(documentID) == 0 {
			documentID = CreateDocument(client, name)
		} else {
			logrus.Infof("Skipping creating Document %s", name)
		}

		file, err := os.OpenFile(filePath, os.O_RDONLY, 0700)
		if err != nil {
			logrus.Errorf("Error opening file `%s`: %v", filePath, err)
			continue
		}

		if !hasFile {
			CreateFile(client, file, strings.ReplaceAll(name, "%", ""), documentID)
		}
	}

	for _, doc := range documents.GetArray("documents") {
		removeDoc := true
		docTitle := string(doc.GetStringBytes("title"))
		documentID := string(doc.GetStringBytes("id"))

		for _, checkFile := range files {
			fileName := getFilename(checkFile)

			if fileName != docTitle {
				continue
			}

			removeDoc = false
		}

		if removeDoc {
			logrus.Infof("Should remove %s", docTitle)
			DeleteDocument(client, documentID)
		}
	}
}

func CreateFile(client *resty.Client, file *os.File, fileName, documentID string) {
	resp, err := client.R().
		SetFormData(map[string]string{
			"id": documentID,
		}).
		SetFileReader("file", fileName, file).
		Put(ApiAppFile)
	if err != nil {
		logrus.Fatalf("Error uploading file: %v", err)
	}

	value, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		logrus.Fatalf("Error parsing json: %v", err)
	}

	if string(value.GetStringBytes("type")) == "FileError" {
		logrus.Info(value.String())
		logrus.Fatal("Error uploading file")
	}

	logrus.Infof("Upload complete. Status: %s", string(value.GetStringBytes("status")))
}

func CreateDocument(client *resty.Client, name string) string {
	resp, err := client.R().SetFormDataFromValues(map[string][]string{
		"title":    {name},
		"language": {Language},
		"tags":     Tags,
	}).Put(ApiAppDocument)
	if err != nil {
		logrus.Fatalf("Error creating document: %v", err)
	}

	value, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		logrus.Fatalf("Error parsing json: %v", err)
	}

	logrus.Infof("Created Document")

	return string(value.GetStringBytes("id"))
}

func DeleteDocument(client *resty.Client, documentID string) {
	_, err := client.R().Delete(ApiAppDocument + "/" + documentID)
	if err != nil {
		logrus.Fatalf("Error creating document: %v", err)
	}

	logrus.Infof("Deleted Document")
}

func GetDocuments(client *resty.Client) *fastjson.Value {
	resp, err := client.R().Get(ApiAppDocumentList)
	if err != nil {
		logrus.Fatalf("Error fetching document list: %v", err)
	}

	value, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		logrus.Fatalf("Error parsing json: %v", err)
	}

	return value
}

func GatherFiles() []string {
	var files []string
	err := filepath.Walk(Path, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		files = append(files, path)
		logrus.Infof("found file %s - %d bytes", path, info.Size())

		return nil
	})
	if err != nil {
		logrus.Fatalf("Error traversing folder: %v", err)
	}

	return files
}

func CheckServer(client *resty.Client) {
	resp, err := client.R().Get(ApiApp)
	if err != nil {
		logrus.Fatalf("Invalid BaseURL: %v", err)
	}

	value, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		logrus.Fatalf("Error parsing json: %v", err)
	}

	logrus.Infof("Connection Successful. Version: %s", string(value.GetStringBytes("current_version")))
}

func Login(client *resty.Client) {
	resp, err := client.R().SetFormData(map[string]string{
		"username": "admin",
		"password": "admin",
		"remember": "true",
	}).Post(ApiUserLogin)
	if err != nil {
		logrus.Fatalf("Error logging in: %v", err)
	}

	if resp.StatusCode() != 200 {
		logrus.Fatal("Invalid Credentials")
	}
}

func GetTags(client *resty.Client) *fastjson.Value {
	resp, err := client.R().Get(ApiAppTagList)
	if err != nil {
		logrus.Fatalf("Error fetching tag list: %v", err)
	}

	value, err := fastjson.ParseBytes(resp.Body())
	if err != nil {
		logrus.Fatalf("Error parsing json: %v", err)
	}

	return value
}

func getFilename(s string) string {
	s = path.Base(s)                // Get plain filename
	s = s[:len(s)-len(path.Ext(s))] // Remove extension from filename
	return s
}
