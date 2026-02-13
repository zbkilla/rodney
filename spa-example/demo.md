# SPA Hash Routing with Rodney

*2026-02-13T17:29:01Z*

This demo builds a vanilla JavaScript single-page app that uses hash-based routing (`#home`, `#page2`, `#page3`) and then uses rodney to verify that fragment URLs work correctly — both opening a URL that starts on a specific fragment page and navigating between pages within the app.

## The SPA

Here is the complete single-page app — one HTML file with a JS router that listens for `hashchange` events and swaps the content:

```bash
cat spa-example/index.html
```

```output
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>SPA App</title>
  <style>
    body { font-family: sans-serif; max-width: 600px; margin: 2rem auto; }
    nav { display: flex; gap: 1rem; margin-bottom: 2rem; border-bottom: 1px solid #ccc; padding-bottom: 1rem; }
    nav a { text-decoration: none; color: #0066cc; }
    nav a:hover { text-decoration: underline; }
    h1 { color: #333; }
  </style>
</head>
<body>
  <nav>
    <a href="#home" id="nav-home">Home</a>
    <a href="#page2" id="nav-page2">Page 2</a>
    <a href="#page3" id="nav-page3">Page 3</a>
  </nav>
  <div id="content"></div>
  <script>
    var pages = {
      home:  '<h1>Home Page</h1><p>Welcome to the SPA</p>',
      page2: '<h1>Page Two</h1><p>This is the second page</p>',
      page3: '<h1>Page Three</h1><p>This is the third page</p>'
    };
    function route() {
      var hash = location.hash.replace('#', '') || 'home';
      document.getElementById('content').innerHTML = pages[hash] || '<h1>Not Found</h1>';
      document.title = 'SPA - ' + hash;
    }
    window.addEventListener('hashchange', route);
    route();
  </script>
</body>
</html>
```

## Serve it and start rodney

Start a local HTTP server and launch Chrome via rodney:

```bash
./rodney start >/dev/null 2>&1 && echo 'Chrome started'
```

```output
Chrome started
```

## Test 1: Open directly at a fragment URL

Open the SPA at `#page2` — rodney should load the page and the JS router should render the Page Two content:

```bash
./rodney open "http://127.0.0.1:18090/#page2"
```

```output
SPA - page2
```

```bash
./rodney url
```

```output
http://127.0.0.1:18090/#page2
```

```bash
./rodney text h1
```

```output
Page Two
```

The URL includes the `#page2` fragment, the title is `SPA - page2`, and the heading says "Page Two". The router correctly handled the fragment on initial page load.

## Test 2: Open at a different fragment

Try `#page3` to make sure it wasn't a fluke:

```bash
./rodney open "http://127.0.0.1:18090/#page3"
```

```output
SPA - page3
```

```bash
./rodney text h1
```

```output
Page Three
```

## Test 3: Navigate within the SPA by clicking links

Start from the home page (no fragment), then click through the nav links and verify each transition:

```bash
./rodney open "http://127.0.0.1:18090/"
```

```output
SPA - home
```

```bash
./rodney text h1
```

```output
Home Page
```

Now click the "Page 2" nav link:

```bash
./rodney click "#nav-page2"
```

```output
Clicked
```

```bash
./rodney url
```

```output
http://127.0.0.1:18090/#page2
```

```bash
./rodney text h1
```

```output
Page Two
```

Click to "Page 3":

```bash
./rodney click "#nav-page3"
```

```output
Clicked
```

```bash
./rodney url && ./rodney text h1
```

```output
http://127.0.0.1:18090/#page3
Page Three
```

And back to "Home":

```bash
./rodney click "#nav-home"
```

```output
Clicked
```

```bash
./rodney url && ./rodney text h1
```

```output
http://127.0.0.1:18090/#home
Home Page
```

## Results

Rodney handles SPA hash routing correctly:

- **Opening a fragment URL directly** — `rodney open http://.../#page2` loads the page and the JS router renders the correct content based on the fragment.
- **Navigating within the SPA** — clicking hash links (`<a href="#page2">`) triggers `hashchange` events, the router swaps content, and rodney can read the updated URL, title, and DOM at each step.
- **No full page reload** — the browser stays on the same document throughout; only the hash and DOM content change.

```bash
./rodney stop >/dev/null 2>&1 && echo "Done"
```

```output
Done
```
