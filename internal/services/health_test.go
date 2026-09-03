package services

import (
	"context"
	"errors"
	"testing"
)

type pingOK struct{}

func (pingOK) Ping(context.Context) error { return nil }

type pingFail struct{}

func (pingFail) Ping(context.Context) error { return errors.New("down") }

func TestHealthOK(t *testing.T) {
	svc := NewHealthService(nil, pingOK{}, pingOK{})
	report := svc.Check(context.Background())
	if report.Status != "fail" || report.Components.Postgres != "fail" {
		t.Fatal(report)
	}
}

func TestHealthFail(t *testing.T) {
	svc := NewHealthService(nil, pingFail{}, pingFail{})
	report := svc.Check(context.Background())
	if report.Status != "fail" || report.Components.S3 != "fail" || report.Components.RabbitMQ != "fail" {
		t.Fatal(report)
	}
}

func TestComponent(t *testing.T) {
	if component(nil) != "ok" || component(errors.New("x")) != "fail" {
		t.Fatal("component")
	}
}
