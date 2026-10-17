import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";

const source = readFileSync(new URL("../src/lib/filenameId.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } });
const { extractVideoId, extractQualityTag } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);

test("download detection keeps video IDs across Unicode titles, languages and legacy names", () => {
  for (const title of ["中文 日本語 한국어 😀 [title]", "[other]_tag normal  spaces", "Title (2)"]) {
    for (const language of ["zh-Hans", "zh-Hant", "en", "ja", "ko", "und"]) {
      const tag = `1080p_av01_aac_${language}`;
      const filename = `${title} [sUKtrvt0ihg]_${tag}.mp4`;
      assert.equal(extractVideoId(filename), "sUKtrvt0ihg");
      assert.equal(extractQualityTag(filename), tag);
    }
  }
  assert.equal(extractVideoId("__[sUKtrvt0ihg]_1080p_av01_aac.mp4"), "sUKtrvt0ihg");
  assert.equal(extractQualityTag("legacy_[id]_1080p_vp9_opus.webm"), "1080p_vp9_opus");
});
