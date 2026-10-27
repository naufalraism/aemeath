import { createClient, ApiError } from "./generated/client";
import * as S from "./generated/schemas";
import { createHooks } from "./generated/hooks";

const base = "http://localhost:38123";
const api = createClient({ baseUrl: base });

const valid = { name: "Bobby", email: "a@b.co", role: "admin", age: 30, level: 2, tags: ["x"], code: "ABC-12", address: { city: "Rome" }, nickname: null };
const cases: Record<string, any> = {
  "valid": valid,
  "short name": { ...valid, name: "ab" },
  "long name": { ...valid, name: "abcdefghijk" },
  "bad email": { ...valid, email: "nope" },
  "bad role": { ...valid, role: "root" },
  "age 17": { ...valid, age: 17 },
  "age 119": { ...valid, age: 119 },
  "age 120 (exclusive)": { ...valid, age: 120 },
  "age null (required)": { ...valid, age: null },
  "age missing": (({ age, ...r }) => r)(valid),
  "level 9": { ...valid, level: 9 },
  "3 tags": { ...valid, tags: ["a", "b", "c"] },
  "bad code": { ...valid, code: "abc" },
  "address.city too short": { ...valid, address: { city: "R" } },
  "address omitted": (({ address, ...r }) => r)(valid),
  "optional fields omitted": { name: "Bobby", email: "a@b.co", age: 18, address: { city: "Rome" } },
  "nickname null": { ...valid, nickname: null },
};

function zodFields(issues: { path: (string | number)[] }[]): string[] {
  return [...new Set(issues.map((i) => i.path.map(String).join(".")))].sort();
}

async function main() {
  let mismatches = 0;
  for (const [name, body] of Object.entries(cases)) {
    const res = await fetch(base + "/signup", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const serverOk = res.status === 201;
    const serverFields = serverOk ? [] : [...new Set(((await res.json()).errors as { field: string }[]).map((e) => e.field))].sort();
    const parsed = S.postSignupBodySchema.safeParse(body);
    const zodFieldsList = parsed.success ? [] : zodFields(parsed.error.issues);
    const sameDecision = serverOk === parsed.success;
    const samePaths = JSON.stringify(serverFields) === JSON.stringify(zodFieldsList);
    if (!sameDecision) mismatches++;
    const same = sameDecision;
    console.log(`${!sameDecision ? "MISMATCH" : samePaths ? "OK      " : "OK(path)"} ${name.padEnd(26)} server=${serverOk ? "accept" : "reject " + serverFields} | zod=${parsed.success ? "accept" : "reject " + zodFieldsList}`);
  }

  // Typed client against the live server.
  const list = await api.getItems({ query: { tag: ["a", "b"], limit: 0 }, headers: { "X-Trace": "t1" } });
  console.log("client getItems ->", JSON.stringify(list), "| response matches UserSchema:", S.UserSchema.array().safeParse(list).success);
  const created = await api.postSignup({ body: valid as never });
  console.log("client postSignup ->", JSON.stringify(created));
  try { await api.postSignup({ body: { ...valid, name: "x" } as never }); } catch (e) {
    const err = e as ApiError;
    console.log("client 422 ->", err instanceof ApiError, err.status, err.message, JSON.stringify(err.errors));
  }
  console.log("client delete (204) ->", await api.deleteItemsById({ path: { id: 3 } }));
  console.log("hooks created:", Object.keys(createHooks(api)).join(", "));
  if (mismatches) { console.log(`\n${mismatches} MISMATCH(ES)`); process.exit(1); }
  console.log("\nthe server and the generated Zod schemas made the same accept/reject decision in every case");
}
main();
