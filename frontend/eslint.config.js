import eslint from "@eslint/js";
import typescriptParser from "@typescript-eslint/parser";

export default [
  eslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}", "**/*.js"],
    languageOptions: {
      parser: typescriptParser,
      parserOptions: { ecmaVersion: "latest", sourceType: "module" },
      globals: { document: "readonly" },
    },
  },
];
