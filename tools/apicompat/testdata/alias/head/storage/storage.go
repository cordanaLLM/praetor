package storage

import (
	storage2 "example.com/storage/core/storage"
)

type (
	Bucket = storage2.Bucket
	Object = storage2.Object
)

type LocalBucket struct{}

func (b *LocalBucket) Get(name string) (Object, error) {
	return Object{Name: name}, nil
}

func Connect(b Bucket) Object {
	return Object{}
}
