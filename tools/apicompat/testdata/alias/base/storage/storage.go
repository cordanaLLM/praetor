package storage

type Bucket interface {
	Get(name string) (Object, error)
}

type Object struct {
	Name string
}

type LocalBucket struct{}

func (b *LocalBucket) Get(name string) (Object, error) {
	return Object{Name: name}, nil
}

func Connect(b Bucket) Object {
	return Object{}
}
