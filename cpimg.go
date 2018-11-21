package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

func cpimg(u *url.URL) (*url.URL, error) {
	parts := strings.Split(u.Path, "/")
	name := parts[len(parts)-1]

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("getting original image %s %s", u.String(), err)
	}
	orig, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading image %s %s", u.String(), err)
	}

	sv := s3manager.NewUploader(awsSession)
	_, err = sv.Upload(&s3manager.UploadInput{
		Bucket:      aws.String("imgs.rumblegoods.com"),
		Key:         aws.String(name),
		Body:        bytes.NewBuffer(orig),
		ContentType: aws.String(http.DetectContentType(orig)),
	})
	if err != nil {
		return nil, fmt.Errorf("sending img to s3 %s %s", name, err)
	}
	return url.Parse(fmt.Sprintf("http://imgs.rumblegoods.com/%s", name))
}
