package license

// activationGetter matches the encrypted store methods Billy needs to load a
// cached Lemon Squeezy activation without importing the store package here.
type activationGetter interface {
	GetEncrypted(key string) ([]byte, error)
}

// LoadCached reconstructs the last validated license from encrypted local
// storage. It returns nil when no activation has been cached yet.
func LoadCached(src activationGetter) (*License, error) {
	if src == nil {
		return nil, nil
	}

	actBytes, err := src.GetEncrypted("ls_activation")
	if err != nil || len(actBytes) == 0 {
		return nil, err
	}

	act, err := UnmarshalActivation(actBytes)
	if err != nil {
		return nil, err
	}
	return act.ToLicense(), nil
}
