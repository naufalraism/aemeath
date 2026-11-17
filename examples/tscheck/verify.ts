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

function zodFields(issues: { path: PropertyKey[] }[]): string[] {
  return [...new Set(issues.map((i) => i.path.map(String).join(".")))].sort();
}

// ---- inputs for POST /strict. Each one is sent to the server and parsed by
// the generated Zod schema; both must make the same decision. The list covers
// the cases where the two used to disagree: empty values, null, formats,
// patterns Go and JavaScript read differently, and nested values.
const ok = { name: "abc", short: "a", age: 1, flag: true, opt_ptr: 1, tags: ["a"], kind: 1, status: "a", email: "a@b.co",
  url: "https://a.co", uuid: "123e4567-e89b-42d3-a456-426614174000", ratio: 0.5, children: [{ name: "ab" }],
  when: "2026-11-16T22:00:00+07:00", omit: "abc", code_i: "AB", code_n: "12-x", code_a: "ab" };
const without = (k: string) => { const { [k]: _, ...rest } = ok as Record<string, unknown>; return rest; };
const strict: Record<string, unknown> = {
  "all valid": ok,
  "only required": { name: "abc", age: 1, flag: true },
  // "was a value sent?": a present key is validated even when empty; missing or null is not.
  "email empty string": { ...ok, email: "" },
  "url empty string": { ...ok, url: "" },
  "uuid empty string": { ...ok, uuid: "" },
  "status empty string": { ...ok, status: "" },
  "omitempty field empty": { ...ok, omit: "" },
  "omitempty field too short": { ...ok, omit: "ab" },
  "kind 0": { ...ok, kind: 0 },
  "ratio 0": { ...ok, ratio: 0 },
  "tags []": { ...ok, tags: [] },
  "tags null": { ...ok, tags: null },
  "tags missing": without("tags"),
  "kind missing": without("kind"),
  "ratio missing": without("ratio"),
  "email missing": without("email"),
  "opt_ptr null": { ...ok, opt_ptr: null },
  "opt_ptr 0": { ...ok, opt_ptr: 0 },
  "children null": { ...ok, children: null },
  "children [{}]": { ...ok, children: [{}] },
  "children name too short": { ...ok, children: [{ name: "ab" }, { name: "a" }] },
  // required
  "flag false": { ...ok, flag: false },
  "age null": { ...ok, age: null },
  "name empty": { ...ok, name: "" },
  // lengths count characters, not bytes or UTF-16 units
  "name 3 emoji (max 5)": { ...ok, name: "😀😀😀" },
  "short 2 emoji (max 3)": { ...ok, short: "😀😀" },
  "name 6 letters": { ...ok, name: "abcdef" },
  // email
  "email without dot in domain": { ...ok, email: "a@b" },
  "email address literal": { ...ok, email: "a@[127.0.0.1]" },
  "email non-ASCII": { ...ok, email: "jösé@b.co" },
  "email display name": { ...ok, email: "Ann <a@b.co>" },
  "email trailing dot": { ...ok, email: "a@b.co." },
  "email double dot": { ...ok, email: "a..b@c.co" },
  // url
  "url ftp": { ...ok, url: "ftp://a.co/x" },
  "url mailto": { ...ok, url: "mailto:a@b.co" },
  "url javascript": { ...ok, url: "javascript:alert(1)" },
  "url without host": { ...ok, url: "http://" },
  "url scheme-relative": { ...ok, url: "//a.co" },
  "url with space": { ...ok, url: "https://a.co/a b" },
  "url upper-case scheme": { ...ok, url: "HTTPS://A.CO/x" },
  // uuid
  "uuid upper case": { ...ok, uuid: "123E4567-E89B-42D3-A456-426614174000" },
  "uuid nil": { ...ok, uuid: "00000000-0000-0000-0000-000000000000" },
  "uuid version 0": { ...ok, uuid: "123e4567-e89b-02d3-a456-426614174000" },
  "uuid without dashes": { ...ok, uuid: "123e4567e89b42d3a456426614174000" },
  "uuid in braces": { ...ok, uuid: "{123e4567-e89b-42d3-a456-426614174000}" },
  // time
  "when Z": { ...ok, when: "2026-11-16T22:00:00Z" },
  "when nanoseconds": { ...ok, when: "2026-11-16T22:00:00.123456789+07:00" },
  "when date only": { ...ok, when: "2026-11-16" },
  "when offset without colon": { ...ok, when: "2026-11-16T22:00:00+0700" },
  // patterns: Go (RE2) syntax translated for JavaScript
  "code_i lower case": { ...ok, code_i: "ab" },
  "code_i digits": { ...ok, code_i: "12" },
  "code_n no match": { ...ok, code_n: "x-12" },
  "code_n four digits (still contains a match)": { ...ok, code_n: "1234-x" },
  "code_a with newline": { ...ok, code_a: "ab\n" },
  "code_a prefix only": { ...ok, code_a: "abc" },
  // types
  "age 1.5": { ...ok, age: 1.5 },
  "age as string": { ...ok, age: "1" },
  "kind 3": { ...ok, kind: 3 },
};

async function checkStrict(): Promise<number> {
  let bad = 0;
  for (const [name, body] of Object.entries(strict)) {
    const res = await fetch(base + "/strict", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const serverOk = res.status === 200;
    const parsed = S.postStrictBodySchema.safeParse(body);
    if (serverOk === parsed.success) continue;
    bad++;
    const detail = serverOk ? parsed.error!.issues.map((i) => i.path.join(".") + ": " + i.message).join("; ") : JSON.stringify((await res.json()).errors ?? "bad request");
    console.log(`MISMATCH strict: ${name.padEnd(34)} server=${res.status} zod=${parsed.success ? "accept" : "reject"} :: ${detail}`);
  }
  console.log(`strict inputs: server and Zod agree on ${Object.keys(strict).length - bad} of ${Object.keys(strict).length}`);
  return bad;
}

// A response must satisfy its own generated schema, for a zero value (nil
// slices, maps and pointers) and for a filled one.
async function checkWire(): Promise<number> {
  let bad = 0;
  for (const which of ["zero", "full"]) {
    const data = await (await fetch(base + "/wire/" + which)).json();
    const parsed = S.WireSchema.safeParse(data);
    if (parsed.success) { console.log(`wire/${which}: the response matches its schema`); continue; }
    bad++;
    for (const i of parsed.error.issues) console.log(`MISMATCH wire/${which}: ${i.path.join(".")} | ${i.message}`);
  }
  return bad;
}

async function main() {
  let mismatches = (await checkWire()) + (await checkStrict());
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
  const listOk = S.UserSchema.array().safeParse(list).success;
  console.log("client getItems ->", JSON.stringify(list), "| response matches UserSchema:", listOk);
  if (!listOk) mismatches++; // this used to be printed only
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
