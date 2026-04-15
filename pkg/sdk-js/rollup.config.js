import resolve from "@rollup/plugin-node-resolve";
import commonjs from "@rollup/plugin-commonjs";
import terser from "@rollup/plugin-terser";
import typescript from "@rollup/plugin-typescript";

export default {
  input: "src/index.ts",
  output: {
    file: "dist/traceforge-sdk.js",
    format: "umd",
    name: "TraceForgeSDK",
    sourcemap: true,
    exports: "named",
  },
  plugins: [
    resolve({
      extensions: [".ts", ".js"],
    }),
    commonjs(),
    typescript({
      tsconfig: "./tsconfig.json",
      declaration: false,
    }),
    terser(),
  ],
};
