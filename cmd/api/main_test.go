package main

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestIdentAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ident API Suite")
}

var _ = Describe("Main", func() {

})
