package lib

// MaxAttempts caps how many times Retry will try before giving up.
const MaxAttempts = 3

// Retry calls fn until it succeeds or MaxAttempts is reached.
func Retry(fn func() error) error {
	var err error
	for i := 0; i < MaxAttempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}
