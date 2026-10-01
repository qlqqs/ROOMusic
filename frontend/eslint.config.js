import eslint from "@eslint/js";
import typescriptPlugin from "@typescript-eslint/eslint-plugin";
import reactHooks from "eslint-plugin-react-hooks";

export default [
  { ignores: ["dist/**", "node_modules/**", "playwright-report/**", "test-results/**"] },
  eslint.configs.recommended,
  ...typescriptPlugin.configs["flat/recommended"],
  reactHooks.configs.flat.recommended,
  {
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      "@typescript-eslint/no-non-null-assertion": "error",
      // 请求只经 shared/api 发出：组件与 hook 不直接调用 fetch。
      "no-restricted-globals": ["error", { name: "fetch", message: "请经 shared/api 的 apiRequest 发送请求。" }],
      "no-restricted-properties": [
        "error",
        { object: "window", property: "fetch", message: "请经 shared/api 的 apiRequest 发送请求。" },
        { object: "globalThis", property: "fetch", message: "请经 shared/api 的 apiRequest 发送请求。" },
      ],
    },
  },
  {
    files: ["src/shared/api/request.ts"],
    rules: { "no-restricted-globals": "off", "no-restricted-properties": "off" },
  },
];
