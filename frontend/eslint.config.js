import eslint from "@eslint/js";
import typescriptPlugin from "@typescript-eslint/eslint-plugin";
import typescriptParser from "@typescript-eslint/parser";

export default [
	{ ignores: ["dist/**", "node_modules/**"] },
	eslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}", "**/*.js"],
    languageOptions: {
      parser: typescriptParser,
      parserOptions: { ecmaVersion: "latest", sourceType: "module" },
      globals: { document: "readonly", fetch: "readonly", RequestInit: "readonly" },
    },
    plugins: { "@typescript-eslint": typescriptPlugin },
    rules: {
      "no-unused-vars": "off",
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
    },
  },
];
