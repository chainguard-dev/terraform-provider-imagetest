package sshhost

import "strings"

// testPrivateKey is a throwaway ed25519 key generated for this test.
var testPrivateKey = strings.TrimSpace(`
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACCi1kY2RDYjHyMLO/IC6FIja7ipdJemIzcHHYUobcq8XgAAAIjVelYH1XpW
BwAAAAtzc2gtZWQyNTUxOQAAACCi1kY2RDYjHyMLO/IC6FIja7ipdJemIzcHHYUobcq8Xg
AAAED1zq6jd0yfUKQAo4r1rc1wOiHT486tFhnbXnvM9ac9VKLWRjZENiMfIws78gLoUiNr
uKl0l6YjNwcdhShtyrxeAAAABHRlc3QB
-----END OPENSSH PRIVATE KEY-----
`) + "\n"
