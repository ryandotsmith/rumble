// +build !aws

package main

func dburl() string {
	return "postgres:///x?sslmode=disable"
}
