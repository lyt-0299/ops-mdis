const path = require('path');
const webpack = require('webpack');
const HtmlWebpackPlugin = require('html-webpack-plugin');
const CopyWebpackPlugin = require('copy-webpack-plugin');
const TerserPlugin = require('terser-webpack-plugin');

module.exports = {
  entry: './src/index.js',
  output: {
    path: path.resolve(__dirname, 'dist/web'),
    publicPath: 'auto',
    filename: 'js/[name].[contenthash].js',
    clean: true,
  },
  module: {
    rules: [
      {
        test: /\.css$/,
        use: ['style-loader', 'css-loader'],
      },
      {
        test: /\.less$/,
        use: [
          'style-loader',
          'css-loader',
          {
            loader: 'less-loader',
            options: {
              lessOptions: {
                javascriptEnabled: true,
              },
              additionalData: `@import "${path.resolve(__dirname, 'src/styles/variables.less')}";`,
            },
          },
        ],
      },
      {
        test: /\.(js|jsx)$/,
        use: 'babel-loader',
        exclude: /node_modules/,
      },
      {
        test: /\.(png|jpe?g|gif|svg)$/,
        type: 'asset/resource', // 将文件作为资源加载，并返回 URL
        generator: {
          filename: 'media/[name].[hash][ext]',
        },
      },
    ],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
    extensions: ['.js', '.jsx'],
    mainFiles: ['index'],
  },
  plugins: [
    new webpack.DefinePlugin({
      'process.env.NODE_ENV': JSON.stringify(process.env.NODE_ENV),
    }),
    new HtmlWebpackPlugin({
      template: 'index.html',
      favicon: './src/assets/settings.svg',
    }),
    new CopyWebpackPlugin({
      patterns: [
        {
          from: path.resolve(__dirname, 'locale'), // 复制 locale 文件夹
          to: path.resolve(__dirname, 'dist/web/locale'),
        },
      ],
    }),
  ],
  optimization: {
    minimize: true,
    minimizer: [
      new TerserPlugin({
        terserOptions: {
          format: {
            comments: false,
          },
        },
        extractComments: false,
      }),
    ],
  },
  devServer: {
    // 本地开发确认api地址为相对路径，绝对路径的时候不会走代理
    proxy: [
      {
        context: ['/api'],
        target: 'http://172.17.30.50:48881',
        changeOrigin: true,
        pathRewrite: { '^/api': '/api' },
      },
    ],
    historyApiFallback: {
      index: '/web/index.html',
    },
    static: {
      directory: path.join(__dirname, 'dist/web'),
      publicPath: '/web',
    },
    open: true,
    port: 3000,
  },
};
