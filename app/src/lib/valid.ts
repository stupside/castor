import type {
  DescMessage,
  MessageShape,
  MessageValidType,
} from "@bufbuild/protobuf";
import { createValidator } from "@bufbuild/protovalidate";
import { Code, ConnectError } from "@connectrpc/connect";

const validator = createValidator();

// A reply outside the contract is the server's fault, never a value the app reads.
export function valid<Desc extends DescMessage>(
  schema: Desc,
  message: MessageShape<Desc>,
): MessageValidType<Desc> {
  const result = validator.validate(schema, message);
  if (result.kind === "valid") return result.message;
  throw new ConnectError(
    "Castor answered outside its contract.",
    Code.Internal,
    undefined,
    undefined,
    result.error,
  );
}
