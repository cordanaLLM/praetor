// A function assigned to a prototype is not a header the scanner recognises, so its call
// through this goes undecided.
function List() {}

List.prototype.walk = function () {
  return this.walk();
};
