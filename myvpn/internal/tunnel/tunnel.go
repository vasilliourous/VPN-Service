package tunnel

type Manager interface {
	Setup() error
	Teardown() error
	AddRoute(dest, gateway string) error
	RemoveRoute(dest string) error
}
