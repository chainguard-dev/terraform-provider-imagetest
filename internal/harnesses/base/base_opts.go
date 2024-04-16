package base

type RegistryAuthOpt struct {
	Username string
	Password string
	Auth     string
}

type RegistryTlsOpt struct {
	CertFile string
	KeyFile  string
	CaFile   string
}
