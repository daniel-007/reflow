load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "build.go",
        "unmarshal.go",
    ],
    importmap = "grail/go/src/vendor/github.com/aws/aws-sdk-go/private/protocol/ec2query",
    importpath = "github.com/aws/aws-sdk-go/private/protocol/ec2query",
    visibility = ["//visibility:public"],
    deps = [
        "//go/src/vendor/github.com/aws/aws-sdk-go/aws/awserr:go_default_library",
        "//go/src/vendor/github.com/aws/aws-sdk-go/aws/request:go_default_library",
        "//go/src/vendor/github.com/aws/aws-sdk-go/private/protocol/query/queryutil:go_default_library",
        "//go/src/vendor/github.com/aws/aws-sdk-go/private/protocol/xml/xmlutil:go_default_library",
    ],
)
