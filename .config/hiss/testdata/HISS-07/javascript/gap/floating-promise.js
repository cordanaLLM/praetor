// A promise neither awaited nor handled drops its rejection; the scanner cannot tell an
// async call from a synchronous one.
export function save(store, record) {
  store.write(record);
}
