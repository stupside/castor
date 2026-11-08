import assert from "node:assert/strict";
import test from "node:test";
import {
  Field_Kind,
  FieldSchema,
  FileDescriptorProtoSchema,
  TypeSchema,
  ValueSchema,
} from "@bufbuild/protobuf/wkt";
import { fromForm } from "../src/lib/form.ts";

const form = (entries) => {
  const f = new FormData();
  for (const [k, v] of entries) f.append(k, v);
  return f;
};

test("inputs fill the fields their names path to, typed by the schema", () => {
  const field = fromForm(
    FieldSchema,
    form([
      ["kind", "TYPE_STRING"],
      ["number", "7"],
      ["packed", "on"],
      ["name", " title "],
      ["jsonName", ""],
      ["$ACTION_ID_1", "react"],
    ]),
  );
  assert.equal(field.kind, Field_Kind.TYPE_STRING);
  assert.equal(field.number, 7);
  assert.equal(field.packed, true);
  assert.equal(field.name, "title");
  assert.equal(field.jsonName, "");
});

test("repeated inputs collect into a list and dotted ones nest", () => {
  const type = fromForm(
    TypeSchema,
    form([
      ["oneofs", "a"],
      ["oneofs", "b"],
      ["sourceContext.fileName", "cast.proto"],
    ]),
  );
  assert.deepEqual(type.oneofs, ["a", "b"]);
  assert.equal(type.sourceContext?.fileName, "cast.proto");
});

test("an input naming a message picks that oneof member", () => {
  const value = fromForm(ValueSchema, form([["structValue", "on"]]));
  assert.equal(value.kind.case, "structValue");
});

test("an input naming a message picks the member its value names, beside fields already read", () => {
  const file = fromForm(
    FileDescriptorProtoSchema,
    form([
      ["options.javaPackage", "castor"],
      ["options", "features"],
    ]),
  );
  assert.equal(file.options?.javaPackage, "castor");
  assert.ok(file.options?.features);
});
