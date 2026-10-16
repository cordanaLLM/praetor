package storage

type Bucket interface {
	Get(name string) (Object, error)
}

type Object struct {
	Name string
}
