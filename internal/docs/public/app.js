'use strict';

const apiURL = new URL('/graphql', window.location.origin).toString();
const external = document.getElementById('open-external');
external.href = 'https://studio.apollographql.com/sandbox/explorer?endpoint=' + encodeURIComponent(apiURL);

if (typeof window.EmbeddedSandbox !== 'function') {
  document.getElementById('fallback').classList.add('visible');
} else {
  // Real Apollo Sandbox, not a custom GraphQL editor. Run requests through
  // the embedding origin so CORS is not required for ordinary HTTP queries.
  new window.EmbeddedSandbox({
    target: '#sandbox',
    initialEndpoint: apiURL,
    endpointIsEditable: false,
    hideCookieToggle: true,
    handleRequest: (url, options) => fetch(url, { ...options, credentials: 'omit' }),
    initialState: {
      document: 'query Posts { posts(first: 10) { edges { node { id title commentCount } } ' +
        'pageInfo { hasNextPage endCursor } } }',
    },
  });
}
