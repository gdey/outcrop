export default {
  build: {
    overwriteDest: true,
  },
  run: {
    target: ["firefox-desktop"],
    startUrl: ["about:debugging#/runtime/this-firefox"],
  },
};
