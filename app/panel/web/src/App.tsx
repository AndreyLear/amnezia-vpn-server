import LoginPage from "@/pages/LoginPage";

function App() {
  if (window.location.pathname === "/login") {
    return <LoginPage />;
  }
  return <div>panel</div>;
}

export default App;
