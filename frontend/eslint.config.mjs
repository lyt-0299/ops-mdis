import babelParser from '@babel/eslint-parser';
import js from '@eslint/js';
// import prettierConfig from 'eslint-config-prettier';
import importPlugin from 'eslint-plugin-import';
// import prettierPlugin from 'eslint-plugin-prettier';
import reactPlugin from 'eslint-plugin-react';
import reactHooksPlugin from 'eslint-plugin-react-hooks';
import simpleImportSortPlugin from 'eslint-plugin-simple-import-sort';

export default [
  {
    name: 'custom-rules',
    files: ['**/*.js', '**/*.jsx'],
    languageOptions: {
      parser: babelParser, // 使用 babel-eslint 解析器
    },
    plugins: {
      js,
      react: reactPlugin,
      'react-hooks': reactHooksPlugin,
      import: importPlugin,
      // prettier: prettierPlugin,
      'simple-import-sort': simpleImportSortPlugin,
    },
    settings: {
      'import/resolver': {
        alias: {
          map: [['@', './src']], // 设置 @ 指向 src 目录
          extensions: ['.js', '.jsx'], // 解析的文件类型
        },
      },
      react: {
        version: 'detect', // 自动检测 React 版本
      },
    },
    rules: {
      'react/jsx-uses-react': 'error',
      'react/jsx-uses-vars': 'error',
      // react-hooks：规则钩子校验为 error，依赖数组缺失为 warn（配合代码内既有 disable 指令）
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      // 使用 simple-import-sort 插件自动排序导入
      'simple-import-sort/imports': [
        'error',
        {
          groups: [
            // react 放在首行，react 和其他外部依赖一起
            ['^react', '^@?\\w'],

            // 内部导入，假设我们使用 @ 和 components 来标记内部模块
            ['^(@|components)(/.*|$)'],

            // 父级导入（即..），确保父级导入排在其他导入之后
            ['^\\.\\.(?!/?$)', '^\\.\\./?$'],

            // 同级导入（即./），确保同级导入排在其他导入之后
            ['^\\./(?=.*/)(?!/?$)', '^\\.(?!/?$)', '^\\./?$'],

            // 样式导入（例如CSS）
            ['^.+\\.?(css)$'],

            // 导入副作用的文件（例如：import 'a.css'），确保它排在其他导入之后
            ['^\\u0000'],
          ],
        },
      ],
      // 排序导出的内容
      'simple-import-sort/exports': 'error',
      // 防止重复导入
      'import/no-duplicates': 'error',
      // 确保导入位于文件顶部
      'import/first': 'error',
      // 导入后确保有一个空行
      'import/newline-after-import': 'error',
    },
  },
  // prettierConfig,
  {
    ignores: ['node_modules', 'dist', 'build'],
  },
];
