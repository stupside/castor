import {
  fromJson,
  ScalarType,
  type DescMessage,
  type JsonObject,
  type MessageShape,
} from "@bufbuild/protobuf";

// Reads inputs named by their JSON path in schema, like target.pinned.address; blank and unknown ones stay unset.
export function fromForm<Desc extends DescMessage>(
  schema: Desc,
  form: FormData,
): MessageShape<Desc> {
  const json: JsonObject = {};
  for (const [path, entry] of form) {
    const value = String(entry).trim();
    if (value) put(json, schema, path.split("."), value);
  }
  return fromJson(schema, json);
}

function put(
  json: JsonObject,
  message: DescMessage,
  [name, ...rest]: string[],
  value: string,
) {
  const field = message.fields.find((f) => f.jsonName === name);
  if (field?.fieldKind === "message") {
    const into = object(json, name);
    if (rest.length) return put(into, field.message, rest, value);
    // An input naming a message picks the member its value names, as preferences.subtitles = disabled does.
    if (
      field.message.fields.some(
        (f) => f.jsonName === value && f.fieldKind === "message",
      )
    )
      object(into, value);
    return;
  }
  if (!field || rest.length) return;
  const held = json[name];
  switch (field.fieldKind) {
    case "list":
      json[name] = [...(Array.isArray(held) ? held : []), value];
      return;
    case "scalar":
      json[name] = field.scalar === ScalarType.BOOL ? value === "on" : value;
      return;
    case "enum":
      json[name] = value;
  }
}

// object is the object under name in json, made empty when there is none yet.
function object(json: JsonObject, name: string): JsonObject {
  const held = json[name];
  if (typeof held === "object" && held !== null && !Array.isArray(held))
    return held;
  const made: JsonObject = {};
  json[name] = made;
  return made;
}
