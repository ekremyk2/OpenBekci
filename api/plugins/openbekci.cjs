// Redocly plugin: OpenBekci API conventions (see api/README.md).
const OPERATION_ID = /^[a-z]+\.[a-z][A-Za-z]+$/;
const SNAKE_CASE = /^[a-z][a-z0-9]*(_[a-z0-9]+)*$/;
const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const PROBLEM_REF = '#/components/responses/Problem';

const OperationIdPattern = () => ({
  Operation: {
    enter(op, { report, location }) {
      if (typeof op.operationId !== 'string' || !OPERATION_ID.test(op.operationId)) {
        report({
          message: `operationId must match ${OPERATION_ID} (<module>.<verb><Noun>), got ${JSON.stringify(op.operationId)}`,
          location: location.child('operationId').key(),
        });
      }
    },
  },
});

const SnakeCaseProperties = () => ({
  Schema: {
    enter(schema, { report, location }) {
      for (const name of Object.keys(schema.properties || {})) {
        if (!SNAKE_CASE.test(name)) {
          report({
            message: `property name "${name}" must be snake_case`,
            location: location.child(['properties', name]).key(),
          });
        }
      }
    },
  },
});

const ProblemResponses = () => ({
  Responses: {
    enter(responses, { report, location }) {
      for (const [code, resp] of Object.entries(responses)) {
        const isError = code === 'default' || /^[45]/.test(code);
        if (!isError) continue;
        if (!resp || !resp.$ref || !resp.$ref.endsWith(PROBLEM_REF) && !resp.$ref.endsWith('responses/Problem')) {
          report({
            message: `response ${code} must be a $ref to the shared Problem response`,
            location: location.child(code).key(),
          });
        }
      }
    },
  },
});

const MutationsDeclareIdempotency = () => ({
  PathItem: {
    enter(item, { report, location }) {
      for (const method of Object.keys(item)) {
        if (MUTATING.has(method) && !item[method]['x-idempotent']) {
          report({
            message: `${method.toUpperCase()} operation must declare x-idempotent: optional|required`,
            location: location.child(method).key(),
          });
        }
      }
    },
  },
});

module.exports = function openbekciPlugin() {
  return {
    id: 'openbekci',
    rules: {
      oas3: {
        'operation-id-pattern': OperationIdPattern,
        'snake-case-properties': SnakeCaseProperties,
        'problem-responses': ProblemResponses,
        'mutations-declare-idempotency': MutationsDeclareIdempotency,
      },
    },
  };
};
