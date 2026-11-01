import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import React from "react"
import ReactDOM from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import App from "./App"
import "./index.css"
import { applyCachedTheme } from "./lib/themes"

// Aniko was called Kumo before 2.0.37: what this browser kept under Kumo's
// names (the theme, sorting, the player's volume…) takes Aniko's.
try {
    for (const key of Object.keys(localStorage)) {
        if (!/^kumo[-.]/.test(key)) continue
        const name = "aniko" + key.slice(4)
        if (localStorage.getItem(name) === null) localStorage.setItem(name, localStorage.getItem(key) ?? "")
        localStorage.removeItem(key)
    }
} catch {
    // no storage (a private window): nothing kept either
}

// The colors used last time, before the settings come from the server.
applyCachedTheme()

const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            refetchOnWindowFocus: false,
            retry: (count, err: any) => count < 1 && err?.status !== 401 && err?.status !== 403 && err?.status !== 404,
        },
    },
})

ReactDOM.createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
        <QueryClientProvider client={queryClient}>
            <BrowserRouter>
                <App />
            </BrowserRouter>
        </QueryClientProvider>
    </React.StrictMode>,
)
