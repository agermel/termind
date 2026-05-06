import { Type, type Static } from "@sinclair/typebox";

/** JSON Schema for a Termind terminal failure event.
 *
 *  This is the shared parameter schema for all five Termind tools.
 *  It uses TypeBox so OpenClaw can generate typed tool-call parameters
 *  and the plugin can validate input at runtime. */
export const FailureEventSchema = Type.Object(
  {
    fingerprint: Type.Optional(Type.String({ maxLength: 64 })),
    summary: Type.String({ maxLength: 300 }),
    command: Type.String({ maxLength: 1000 }),
    severity: Type.Optional(
      Type.Union([
        Type.Literal("info"),
        Type.Literal("warning"),
        Type.Literal("incident"),
      ])
    ),
    exitCode: Type.Optional(Type.Number()),
    cwd: Type.Optional(Type.String({ maxLength: 500 })),
    project: Type.Optional(Type.String({ maxLength: 120 })),
    user: Type.Optional(Type.String({ maxLength: 120 })),
    branch: Type.Optional(Type.String({ maxLength: 120 })),
    gitCommit: Type.Optional(Type.String({ maxLength: 80 })),
    environment: Type.Optional(Type.String({ maxLength: 200 })),
    shell: Type.Optional(Type.String({ maxLength: 40 })),
    tail: Type.Optional(Type.String({ maxLength: 4000 })),
    larkChatId: Type.Optional(
      Type.String({
        maxLength: 160,
        description:
          "Target Feishu/Lark chat id for OpenClaw message tool delivery.",
      })
    ),
    stackTop: Type.Optional(Type.Array(Type.String({ maxLength: 300 }))),
    reportUrl: Type.Optional(Type.String({ maxLength: 500 })),
    occurrences: Type.Optional(Type.Number({ minimum: 0 })),
    affectedUsers: Type.Optional(Type.Number({ minimum: 0 })),
    branchKind: Type.Optional(Type.String({ maxLength: 40 })),
  },
  { additionalProperties: true }
);

export type FailureEvent = Static<typeof FailureEventSchema>;
