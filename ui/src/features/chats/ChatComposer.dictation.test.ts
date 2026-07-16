import { describe, expect, it } from "vitest";

import { insertTranscriptAtSelection } from "./ChatComposer";

describe("insertTranscriptAtSelection", () => {
  it("inserts a transcript at an empty cursor with readable spacing", () => {
    expect(insertTranscriptAtSelection("hello", "world", 5, 5)).toEqual({
      value: "hello world",
      cursor: 11,
    });
    expect(insertTranscriptAtSelection("hello there", "voice", 6, 11)).toEqual({
      value: "hello voice",
      cursor: 11,
    });
  });

  it("does not add a space before dictated punctuation", () => {
    expect(insertTranscriptAtSelection("hello", ", world", 5, 5)).toEqual({
      value: "hello, world",
      cursor: 12,
    });
  });

  it("ignores an empty transcript", () => {
    expect(insertTranscriptAtSelection("draft", "  ", 2, 4)).toEqual({
      value: "draft",
      cursor: 2,
    });
  });
});
