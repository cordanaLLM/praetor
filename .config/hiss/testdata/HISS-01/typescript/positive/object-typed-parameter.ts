// renderFields takes an object-typed parameter and a default, and renders nested fields by
// calling itself, which HISS-01 forbids.
export const renderFields = (fields: { [key: string]: Field }, parentKey = ""): string[] => {
  return Object.entries(fields).flatMap(([key, field]) =>
    field.fields ? renderFields(field.fields, `${parentKey}${key}.`) : [`${parentKey}${key}`],
  );
};
