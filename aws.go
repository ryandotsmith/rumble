// +build aws

package main

import (
	"github.com/aws/aws-sdk-go/service/ssm"
)

func dburl() string {
	k := "/rumble/env/DATABASE_URL"
	d := true
	ps := ssm.New(awsSession)
	res, err := ps.GetParameter(&ssm.GetParameterInput{
		Name:           &k,
		WithDecryption: &d,
	})
	check(err)
	return *res.Parameter.Value
}
