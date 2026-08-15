import HomePage from "@/pages/HomePage";
import LoginPage from "@/pages/LoginPage";
import { Toaster } from "@/components/ui/sonner";

function App() {
  const page =
    window.location.pathname === "/login" ? <LoginPage /> : <HomePage />;

  return (
    <>
      {page}
      <Toaster />
    </>
  );
}

export default App;
